// Package spawn は、パーキングのメモを渡す先として新しい Claude Code のセッションを多重化ツールに起こす (#162)。
//
// 起こすのは daemon のバイナリ (`agents-daemon spawn`)。mod や hook から多重化ツールを叩かない決めごとに合わせる。
// 新しいセッションは repo を cwd にし、session ID をこちらで決めて `claude --session-id <id> --model <model> -- <本文>`
// で起こす。本文は最初のターンとして入るので、peer メッセージの inbox が開くのを待たずに渡せる。
// cc-premise: 2.1.306 cli claude [prompt] は最初のターンの本文、--session-id <uuid> でセッション ID を固定できる
//
// Request.ForkFrom があれば、元のセッションの会話を写した新しいセッションとして起こす (セッションの再起動、#718)。
// 起こす先と包みは ForkFrom の無いときと同じ。
//
// 起こす先は target が決める。足し先 (Request.Session) があればその session のタブ、NewSession なら新しい session、
// どちらも無ければ起こす側の session (Request.Caller) のタブ、Caller も無ければ (park hand、rex の外のアプリ) 新しい
// session。peer-sessions skill の「足し先の既定は起こす側の session のタブ」をここで決める (#636)。指した session に
// 足せなければ、ほかへ倒さず error にする (控えた ID が古いことを黙って隠さない)。
//
// session を起こす口 (Host) は multiplexer.Client に足さず、ここに閉じる。Client は daemon / monitor / doctor / verify が
// pane を見て送るための全部入りで、session の作成はこのコマンドだけが使う (simulate の simulateHost と同じ考え)。
//
// # 起こし方ごとの挙動
//
// 起こし方は 2 通りで、印・閉じる主体・rename の扱いが違う。この一覧の実体はここだけに置き、peer-sessions skill の
// references/rex.md、wrap、reaper、spawnedsession はここを指す (#634)。
//
// 新しい session (Host.NewSession。NewSession の指定か、Session も Caller も無いとき):
//   - 包み: wrap.SessionCommand。claude を起こす前に印 (spawnedsession) を書く。
//   - claude が終わったとき: session の block が包みの 1 つだけなら、包みが session を閉じる。後から開いた pane や
//     タブがあれば自分の pane だけ閉じる。
//   - 空になった後: daemon の reaper が印を見て session を閉じ、印を消す。
//   - 起こした側が畳む口: `spawned-session close <rex_session_id>`。印の無い session は拒否する。
//   - rename: Rex の SessionStart の hook が session の rename を案内し、plugin は止めない (session は peer だけが使う)。
//
// 既存の session のタブ (Host.NewWindow。Session の指定か、無ければ Caller):
//   - 包み: wrap.TabCommand。印を書かず、session を閉じない。
//   - claude が終わったとき: pane が閉じ、最後の pane を失った window を rex が閉じる。session とほかのタブは残る。
//   - 空になった後: 足し先に印があれば (NewSession で起こした session) reaper が閉じる。印が無ければ
//     (利用者が開いた session) 残る。
//   - 起こした側が畳む口: `rex window close -s <rex_session_id> <window_id>`。共有の session なので
//     `spawned-session close` も `session kill` も打たない。
//   - rename: Rex の hook の案内に、plugin の SessionStart hook (shared-rex-session.sh) が「session は rename せず、
//     変えるなら自分の window だけ」の案内を重ねる (#376)。包みが渡す環境変数で、タブで起きたことを知る。
//
// 0 以外で終わったときは、どちらの包みも Enter まで失敗の画面を残す。rename の案内はどちらも claude への依頼で、
// 従うかは claude が決める。起こした側は session を label でなく rex_session_id で指すので、rename されても畳める。
package spawn

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/usadamasa/agents-daemon/internal/multiplexer"
	"github.com/usadamasa/agents-daemon/internal/protocol"
)

// 既定の待ち時間。claude は login shell を経て起きるので数秒かかることがある。
const (
	defaultWaitTimeout  = 15 * time.Second
	defaultWaitInterval = 200 * time.Millisecond
)

// Host は起こす先の多重化ツール。rex の Client が満たす。
type Host interface {
	// NewSession は新しい多重化ツールの session を作り、その最初の pane で argv を起こして pane の置き場を返す。コマンドが
	// 終わり、session にほかの pane が無ければ session を閉じる。異常終了では Enter まで pane を残す (起動の失敗を画面で
	// 読めるようにする)。session ができた時点で返り、コマンドの起動は待たない。
	// label は session と window の名前。
	NewSession(ctx context.Context, label, cwd string, argv []string) (multiplexer.Placement, error)
	// NewWindow は既存の session にタブを足し、その pane で argv を起こして pane の置き場を返す。コマンドが終われば自分の
	// タブだけが閉じ、session とほかのタブは残る。異常終了の扱いと返るときは NewSession と同じ。label はタブの名前。
	NewWindow(ctx context.Context, session, label, cwd string, argv []string) (multiplexer.Placement, error)
	// PaneGet は pane の今の前景 (multiplexer.Client と同じ)。起こした pane で claude が前景になるのを待つのに使う。
	PaneGet(ctx context.Context, paneID string) (multiplexer.Pane, error)
}

// Request は起こすセッションの指定。
type Request struct {
	// Repo は新しいセッションの cwd (絶対パス)。
	Repo string
	// Prompt は最初のターンとして入る本文。PromptFile とどちらか 1 つ。
	Prompt string
	// PromptFile は本文のファイル。相対パスはプロセスの cwd から引く。
	PromptFile string
	// Model は新しいセッションのモデル。空なら Runner.DefaultModel (settings の subagent 用のモデル)。
	Model string
	// Session は足し先の多重化ツールの session (ID か label)。あればその session にタブとして起こす。
	Session string
	// NewSession は新しい session を作る。Session とは併用しない。
	NewSession bool
	// Caller は起こす側の多重化ツールの session。Session も NewSession も無ければ、ここにタブとして起こす。
	// spawn サブコマンドだけが REX_SESSION を入れ、park hand は入れない (rex の中から呼ばれても新しい session に起こす)。
	Caller string
	// SessionID は新しいセッションの ID (UUID)。空なら Runner.NewSessionID。起こす前に依存の辺を書くのに使う。
	SessionID string
	// ForkFrom は会話を写す元のセッションの ID (UUID)。あれば元の会話を持った別の ID のセッションとして起こす
	// (セッションの再起動)。同じ ID で resume しないのは、元のプロセスが閉じるまで transcript を 2 つのプロセスが
	// 書き合い、Remote Control も元に残るため。
	// cc-premise: 2.1.330 cli --resume <id> --fork-session --session-id <uuid> は元の会話を写した <uuid> のセッションになり、SessionStart の source は fork
	ForkFrom string
	// Label は session かタブの名前。空なら repo の basename。
	Label string
}

// Result は起こしたセッション。JSON で stdout に出す。形は protocol の schema が持つ。
type Result = protocol.SpawnResult

// Runner は起こす手段。テストは Host を差し替える。
type Runner struct {
	Host Host
	// NewSessionID は新しいセッションの ID。nil なら newSessionID (UUID v4)。
	NewSessionID func() (string, error)
	// Stat は repo の存在の確認。nil なら os.Stat。
	Stat func(string) (os.FileInfo, error)
	// ReadFile は PromptFile の読み込み。nil なら os.ReadFile。
	ReadFile func(string) ([]byte, error)
	// DefaultModel は Request.Model が空のときのモデル。nil なら空 (起こさない)。
	DefaultModel func() (string, error)
	// WaitTimeout と WaitInterval は、起こした pane で claude が前景になるのを待つ上限と間隔。零なら既定。
	WaitTimeout  time.Duration
	WaitInterval time.Duration
}

// Run は req のセッションを起こし、その pane で claude が前景になってから結果を返す。error は session を作る前か
// 作れなかったときだけ。前景にならなかったときは session ができているので Foreground=false で返す (pane は残るので、
// 起動の失敗は画面で読める)。
func (r Runner) Run(ctx context.Context, req Request) (Result, error) {
	launch, err := r.resolve(req)
	if err != nil {
		return Result{}, err
	}
	argv := []string{"claude"}
	if req.ForkFrom != "" {
		argv = append(argv, "--resume", req.ForkFrom, "--fork-session")
	}
	// `--` は本文が `-` で始まっても flag と読まれないため。
	argv = append(argv, "--session-id", launch.sessionID, "--model", launch.model, "--", launch.prompt)
	placed, err := r.place(ctx, req, launch.label, argv)
	if err != nil {
		return Result{}, err
	}
	res := Result{SessionID: launch.sessionID, PaneID: placed.PaneID, RexSessionID: placed.SessionID, WindowID: placed.WindowID, Foreground: true}
	if err := r.waitForClaude(ctx, placed.PaneID); err != nil {
		if ctx.Err() != nil {
			return Result{}, err
		}
		res.Foreground, res.Detail = false, err.Error()
	}
	return res, nil
}

// place は target の決めた先 (新しい session かタブ) に argv を起こす。
func (r Runner) place(ctx context.Context, req Request, label string, argv []string) (multiplexer.Placement, error) {
	into := target(req)
	if into == "" {
		placed, err := r.Host.NewSession(ctx, label, req.Repo, argv)
		if err != nil {
			return placed, fmt.Errorf("多重化ツールの session を作れない: %w", err)
		}
		return placed, nil
	}
	placed, err := r.Host.NewWindow(ctx, into, label, req.Repo, argv)
	switch {
	case err == nil:
		return placed, nil
	case req.Session != "":
		return placed, fmt.Errorf("多重化ツールの session %s にタブを足せない (session を省くと起こす側の session に足す): %w", req.Session, err)
	default:
		return placed, fmt.Errorf("起こす側の多重化ツールの session %s にタブを足せない: %w", into, err)
	}
}

// target はタブを足す先の session を返す。空なら新しい session を作る。
func target(req Request) string {
	switch {
	case req.NewSession:
		return ""
	case req.Session != "":
		return req.Session
	default:
		return req.Caller
	}
}

// launch は起こすコマンドの中身。既定を埋めた後の値。
type launch struct {
	sessionID, model, label, prompt string
}

// resolve は入力を確かめ、既定を埋める。落ちるときは session を作らない。
func (r Runner) resolve(req Request) (launch, error) {
	if !filepath.IsAbs(req.Repo) {
		return launch{}, fmt.Errorf("repo は絶対パスで指定する: %q", req.Repo)
	}
	stat := r.Stat
	if stat == nil {
		stat = os.Stat
	}
	if _, err := stat(req.Repo); err != nil {
		return launch{}, fmt.Errorf("repo が見つからない: %s: %w", req.Repo, err)
	}
	if req.NewSession && req.Session != "" {
		return launch{}, errors.New("session と new_session は併用しない (足し先が決まらない)")
	}
	prompt, err := r.prompt(req)
	if err != nil {
		return launch{}, err
	}
	model, err := r.model(req)
	if err != nil {
		return launch{}, err
	}
	id, err := r.sessionID(req)
	if err != nil {
		return launch{}, err
	}
	if req.ForkFrom != "" && (!isUUID(req.ForkFrom) || req.ForkFrom == id) {
		return launch{}, fmt.Errorf("fork_from は新しいセッションと別の UUID で指定する: %q", req.ForkFrom)
	}
	label := req.Label
	if label == "" {
		label = filepath.Base(req.Repo)
	}
	return launch{sessionID: id, model: model, label: label, prompt: prompt}, nil
}

// prompt は本文を Prompt か PromptFile から取る。
func (r Runner) prompt(req Request) (string, error) {
	prompt := req.Prompt
	if req.PromptFile != "" {
		if req.Prompt != "" {
			return "", errors.New("prompt と prompt_file は併用しない")
		}
		readFile := r.ReadFile
		if readFile == nil {
			readFile = os.ReadFile
		}
		b, err := readFile(req.PromptFile)
		if err != nil {
			return "", fmt.Errorf("prompt_file %s を読めない: %w", req.PromptFile, err)
		}
		prompt = string(b)
	}
	if strings.TrimSpace(prompt) == "" {
		return "", errors.New("本文が空なので起こさない")
	}
	return prompt, nil
}

// model は Request.Model、無ければ DefaultModel を返す。
func (r Runner) model(req Request) (string, error) {
	if req.Model != "" {
		return req.Model, nil
	}
	model := ""
	if r.DefaultModel != nil {
		m, err := r.DefaultModel()
		if err != nil {
			return "", err
		}
		model = m
	}
	if model == "" {
		return "", errors.New("モデルが無いので起こさない (model を渡すか、Claude Code の settings.json の env に " + SubagentModelKey + " を定義する)")
	}
	return model, nil
}

// sessionID は Request.SessionID (UUID の形を確かめる)、無ければ NewSessionID を返す。
func (r Runner) sessionID(req Request) (string, error) {
	if req.SessionID != "" {
		if !isUUID(req.SessionID) {
			return "", fmt.Errorf("session_id は UUID で指定する: %q", req.SessionID)
		}
		return req.SessionID, nil
	}
	newID := r.NewSessionID
	if newID == nil {
		newID = newSessionID
	}
	id, err := newID()
	if err != nil {
		return "", fmt.Errorf("session ID を作れない: %w", err)
	}
	return id, nil
}

// isUUID は s が 8-4-4-4-12 の 16 進 (小文字) かを返す。Claude Code の --session-id が受ける形。
func isUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
				return false
			}
		}
	}
	return true
}

// waitForClaude は pane で claude が前景になるのを待つ。block ができてから login shell が claude を起こすまでの間は
// PaneGet が not found や shell が前景の Pane を返すので、上限まで数えずに待つ。
func (r Runner) waitForClaude(ctx context.Context, paneID string) error {
	timeout, interval := r.WaitTimeout, r.WaitInterval
	if timeout <= 0 {
		timeout = defaultWaitTimeout
	}
	if interval <= 0 {
		interval = defaultWaitInterval
	}
	deadline := time.Now().Add(timeout)
	for {
		if pane, err := r.Host.PaneGet(ctx, paneID); err == nil && multiplexer.IsClaudeAgent(pane) {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s の間に pane %s で claude が前景にならなかった (pane は残してある。画面で起動の失敗を確かめる)", timeout, paneID)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}
	}
}

// newSessionID は UUID v4 を返す。Claude Code の --session-id が受ける形。
func newSessionID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}
