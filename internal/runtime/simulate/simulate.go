// Package simulate は `simulate` サブコマンドの実装を提供する。使い捨て pane を作り、
// daemon パッケージと同じ検知・判定経路 (monitor パッケージ) をその pane に対してだけ
// 実際に通す end-to-end の検証ツール。読むだけの診断 (doctor と verify の inspect) とは
// 違い、pane の作成と送信を伴う。
package simulate

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/usadamasa/agents-daemon/internal/apppath"
	"github.com/usadamasa/agents-daemon/internal/multiplexer"
	"github.com/usadamasa/agents-daemon/internal/ratelimit"
	"github.com/usadamasa/agents-daemon/internal/runtime/backend"
	"github.com/usadamasa/agents-daemon/internal/runtime/config"
	"github.com/usadamasa/agents-daemon/internal/runtime/detect"
	"github.com/usadamasa/agents-daemon/internal/runtime/monitor"
	"github.com/usadamasa/agents-daemon/internal/sessioncontext"
)

// simulateMetadataSource は simulate が使い捨て block に付ける label。利用者が付けた label と
// 見分けがつくよう、ツール名を含めた固定値にする。
const simulateMetadataSource = "agents-daemon-simulate"

// defaultSimulateScreen は --screen-file を省略した場合に使う内蔵の limit 画面。
// 2026-08-16 に実機 (Claude Code 2.1.224) で観測した文面と同じ形
// (detect/real_sample_test.go の realLimitScreen 参照)。
const defaultSimulateScreen = `  ⏺ Bash(task test)
  ⎿  ✅ テスト完了

⏺ You've hit your session limit · resets 12:30pm (Asia/Tokyo)
  /upgrade to increase your usage limit.

────────────────────────────────────────────────────────────────────────────
❯
────────────────────────────────────────────────────────────────────────────
`

// paneClient は simulate が使い捨て pane に対して使う操作。monitor.Tick へ渡す 3 つ (読む・送る) と、pane が Claude agent に
// 見えたかを待つ PaneGet (#301)。Run が backend.Connect から受ける multiplexer.Client (全部入り) をそのまま渡し、
// scopedClient はこの 4 つだけを所有 pane に閉じる。
type paneClient interface {
	monitor.Client
	// PaneGet は指定 pane の情報を返す。
	PaneGet(ctx context.Context, paneID string) (multiplexer.Pane, error)
}

// Options は Run の呼び出しオプション。
type Options struct {
	Send       bool
	Keep       bool
	ScreenFile string
}

// Result は simulate が通した経路の記録。文字の報告と同じ中身を積み、`simulate --json` はこれをそのまま出す。
// 途中で失敗したときは、そこまでに積んだ分だけを持つ。
type Result struct {
	Backend     string                  `json:"backend"`
	PaneID      string                  `json:"pane_id,omitempty"`
	Agent       string                  `json:"agent,omitempty"`
	AgentStatus multiplexer.AgentStatus `json:"agent_status,omitempty"`
	// Send は dry-run をやめて使い捨て pane へ実際に送ったか。
	Send        bool         `json:"send"`
	ConfigError string       `json:"config_error,omitempty"`
	Ticks       []TickReport `json:"ticks"`
	// Kept は --keep で pane を残したか。Closed は後始末で閉じたか。
	Kept         bool   `json:"kept"`
	Closed       bool   `json:"closed"`
	CleanupError string `json:"cleanup_error,omitempty"`
}

// TickReport は 1 回の monitor.Tick の結果。
type TickReport struct {
	Now       time.Time  `json:"now"`
	Outcome   string     `json:"outcome"`
	Error     string     `json:"error,omitempty"`
	WaitUntil *time.Time `json:"wait_until,omitempty"`
	// WouldSend は dry-run でこの Tick が送るはずだったもの。
	WouldSend []Sent `json:"would_send,omitempty"`
}

// Sent は pane へ送るテキストかキーの 1 回分。
type Sent struct {
	Text string   `json:"text,omitempty"`
	Keys []string `json:"keys,omitempty"`
}

// Run は simulate の本体。使い捨て pane の作成から後始末まで一貫して行う。
// backend は daemon と同じ規則 (REX_SESSION があること) で選ぶ。
func Run(ctx context.Context, w io.Writer, p apppath.Paths, opts Options) (res Result, err error) {
	res = Result{Send: opts.Send, Ticks: []TickReport{}}
	kind, execClient, err := backend.Connect(os.Getenv, sessioncontext.New(p.StateDir()))
	if err != nil {
		return res, err
	}
	res.Backend = string(kind)
	host, err := newSimulateHost(kind, os.Getenv)
	if err != nil {
		return res, err
	}
	if err := execClient.Reachable(ctx); err != nil {
		return res, fmt.Errorf("%s に到達できません: %w", kind, err)
	}

	screenContent := defaultSimulateScreen
	if opts.ScreenFile != "" {
		data, readErr := os.ReadFile(opts.ScreenFile) // #nosec G304 -- CLI 引数で明示指定
		if readErr != nil {
			return res, fmt.Errorf("--screen-file の読み込みに失敗: %w", readErr)
		}
		screenContent = string(data)
	}
	scriptPath, screenPath, removeFiles, err := writeSimulateFiles(simulateScript(host.ProgramStatusCommand()), screenContent)
	if err != nil {
		return res, err
	}
	// script は sh が読みながら実行し、screen は script が起動直後に cat する。どちらも開いた
	// fd があれば削除後も読めるので、pane の後始末と一緒に消してよい。
	defer removeFiles()

	_, _ = fmt.Fprintf(w, "使い捨て pane を作成します (backend=%s)...\n", kind)
	ownedPaneID, err := host.SplitPane(ctx, scriptPath, screenPath)
	if err != nil {
		return res, err
	}
	res.PaneID = ownedPaneID
	_, _ = fmt.Fprintf(w, "  pane %s を作成しました (前景は /bin/sh %s)\n", ownedPaneID, scriptPath)
	// res は名前付きの返り値なので、defer の後始末が書いた欄も呼び手に届く。
	defer cleanupSimulatePane(ctx, w, host, ownedPaneID, opts.Keep, &res)

	if err := prepareSimulatePane(ctx, w, host, execClient, ownedPaneID, screenContent); err != nil {
		return res, err
	}
	err = runSimulateTicks(ctx, w, p, ownedPaneID, execClient, opts, &res)
	return res, err
}

// prepareSimulatePane は使い捨て pane を Claude agent に見せかけ、limit 風の画面が出るまで待つ。
func prepareSimulatePane(ctx context.Context, w io.Writer, host simulateHost, execClient paneClient, ownedPaneID, screenContent string) error {
	marked, err := host.MarkAsClaudeAgent(ctx, ownedPaneID)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(w, "  pane %s: %s\n", ownedPaneID, marked)
	// 登録が backend に載ったことを、daemon と同じ経路 (PaneGet → IsClaudeAgent) で確かめる。
	// rex では script が OSC 7501 を出して record に載るまでの時間が挟まる。
	pane, err := waitForClaudeAgent(ctx, execClient, ownedPaneID, markTimeout)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(w, "  Claude agent として見えました (agent=%s agent_status=%s)\n", pane.Agent, pane.AgentStatus)

	// script の cat が画面に出るのを待つ。コマンドの投入はレンダリング完了を待たずに戻る
	// (実測: 固定 500ms 待機では画面反映前に読んでしまい none 判定になることがあった)。
	if marker := lastNonEmptyLine(screenContent); marker != "" {
		if err := waitForScreenContent(ctx, execClient, ownedPaneID, marker, writeScreenTimeout); err != nil {
			return err
		}
	}
	_, _ = fmt.Fprintln(w, "  limit 風の画面が表示されました")
	return nil
}

// writeSimulateFiles は script と画面テキストを一時ディレクトリに書き、そのパスと削除関数を返す。
func writeSimulateFiles(script, screen string) (scriptPath, screenPath string, remove func(), err error) {
	dir, err := os.MkdirTemp("", "agents-daemon-simulate-")
	if err != nil {
		return "", "", nil, fmt.Errorf("一時ディレクトリの作成に失敗: %w", err)
	}
	remove = func() { _ = os.RemoveAll(dir) }
	scriptPath = filepath.Join(dir, "simulate.sh")
	screenPath = filepath.Join(dir, "screen.txt")
	if err := os.WriteFile(scriptPath, []byte(script), 0o600); err != nil {
		remove()
		return "", "", nil, fmt.Errorf("script の書き込みに失敗: %w", err)
	}
	if err := os.WriteFile(screenPath, []byte(screen), 0o600); err != nil { // #nosec G703 -- screenPath は MkdirTemp のディレクトリ配下の固定名。screen の中身は --screen-file の内容で、パスには使わない
		remove()
		return "", "", nil, fmt.Errorf("画面テキストの書き込みに失敗: %w", err)
	}
	return scriptPath, screenPath, remove, nil
}

// writeScreenTimeout は画面への反映を待つ最大時間。
const writeScreenTimeout = 5 * time.Second

// markTimeout は Claude agent の登録が backend に載るのを待つ最大時間。
const markTimeout = 5 * time.Second

// waitForClaudeAgent は paneID が multiplexer.IsClaudeAgent を満たすまで PaneGet をポーリングし、
// 満たした時点の Pane を返す。
func waitForClaudeAgent(ctx context.Context, client paneClient, paneID string, timeout time.Duration) (multiplexer.Pane, error) {
	deadline := time.Now().Add(timeout)
	var last multiplexer.Pane
	var lastErr error
	for {
		pane, err := client.PaneGet(ctx, paneID)
		if err == nil {
			last = pane
			lastErr = nil
			if multiplexer.IsClaudeAgent(pane) {
				return pane, nil
			}
		} else {
			lastErr = err
		}
		if time.Now().After(deadline) {
			if lastErr != nil {
				return multiplexer.Pane{}, fmt.Errorf("pane %s が %s 以内に Claude agent として見えませんでした (最後の PaneGet の失敗: %w)", paneID, timeout, lastErr)
			}
			return multiplexer.Pane{}, fmt.Errorf("pane %s が %s 以内に Claude agent として見えませんでした (最後の観測: agent=%q agent_status=%s)", paneID, timeout, last.Agent, last.AgentStatus)
		}
		time.Sleep(150 * time.Millisecond)
	}
}

// lastNonEmptyLine は s の最後の空白以外を含む行を trim して返す。--source detection は
// pane の viewport に収まる範囲しか見せない (実測)。中身が viewport より長いと先頭の行から
// 順にスクロールアウトするため、待ち受けの目印には終端側の行を使う方が生き残りやすい。
func lastNonEmptyLine(s string) string {
	lines := strings.Split(s, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if trimmed := strings.TrimSpace(lines[i]); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// waitForScreenContent は paneID の画面に substr が現れるまでポーリングする。
func waitForScreenContent(ctx context.Context, client paneClient, paneID, substr string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastScreen string
	for {
		screen, err := client.PaneRead(ctx, paneID, multiplexer.PaneReadOptions{Lines: 40})
		if err == nil {
			lastScreen = screen
			if strings.Contains(screen, substr) {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("pane %s の画面に %q が %s 以内に反映されませんでした (最後に読めた内容: %q)", paneID, substr, timeout, lastScreen)
		}
		time.Sleep(150 * time.Millisecond)
	}
}

// cleanupSimulatePane は使い捨て pane の後始末をする。ownedPaneID は必ず
// host.SplitPane がこのプロセス自身で作った値であり、呼び出し元にそれ以外の
// pane_id を渡す手段は無い。失敗しても simulate 全体は失敗させず、手動での
// 後始末コマンドを案内するだけに留める。
func cleanupSimulatePane(ctx context.Context, w io.Writer, host simulateHost, ownedPaneID string, keep bool, res *Result) {
	if keep {
		res.Kept = true
		_, _ = fmt.Fprintf(w, "--keep が指定されたため pane %s は残しています (`%s` で手動削除してください)\n", ownedPaneID, host.CloseCommand(ownedPaneID))
		return
	}
	if err := host.ClosePane(ctx, ownedPaneID); err != nil {
		res.CleanupError = err.Error()
		_, _ = fmt.Fprintf(w, "後始末に失敗しました (`%s` で手動削除してください): %v\n", host.CloseCommand(ownedPaneID), err)
		return
	}
	res.Closed = true
	_, _ = fmt.Fprintf(w, "pane %s を閉じました\n", ownedPaneID)
}

// runSimulateTicks は monitor.Tick を daemon と同じ設定で実行し、結果を w へ表示する。
// 1 回目の Tick で limit を検知して待機状態に入った場合、2 回目は起床時刻を過ぎたと
// 仮定した合成の now を渡して再送信の経路 (recoverPane 経由の SendText/SendKeys) まで
// 確認する。実時間では待たない (上限解除まで数時間かかるため)。
// sessionIDOf は使い捨て pane に紐づく Claude セッション ID を引く。取れなければ空 (state 無しとして扱う)。
// 利用上限 state はセッションごとに別ファイルなので、この対応付けが取れないとどの state を読むかが決まらない。
func sessionIDOf(ctx context.Context, client paneClient, paneID string) string {
	pane, err := client.PaneGet(ctx, paneID)
	if err != nil {
		return ""
	}
	return pane.SessionID
}

func runSimulateTicks(ctx context.Context, w io.Writer, p apppath.Paths, ownedPaneID string, execClient paneClient, opts Options, res *Result) error {
	cfg, cfgErr := config.Load(p.ConfigFile())
	if cfgErr != nil {
		res.ConfigError = cfgErr.Error()
		_, _ = fmt.Fprintf(w, "設定の読み込みに失敗、デフォルト値で継続します: %v\n", cfgErr)
		cfg = config.Default()
	}
	classifier, err := detect.NewClassifier(cfg.CustomPatterns)
	if err != nil {
		return fmt.Errorf("分類器の構築に失敗: %w", err)
	}
	store := ratelimit.New(p.StateDir())
	rateState, stateErr := store.Load(sessionIDOf(ctx, execClient, ownedPaneID))
	if stateErr != nil {
		_, _ = fmt.Fprintf(w, "利用上限 state の読み込みに失敗、state 無しとして扱います: %v\n", stateErr)
		rateState = nil
	}
	account, accountErr := store.LatestWindow(time.Now())
	if accountErr != nil {
		_, _ = fmt.Fprintf(w, "アカウント全体の利用上限 state の走査に失敗、無しとして扱います: %v\n", accountErr)
		account = nil
	}

	guarded := newScopedClient(execClient, ownedPaneID)
	var tickClient monitor.Client = guarded
	var wouldSend []Sent
	mode := "dry-run (--send を付けると実際に送信します)"
	if opts.Send {
		mode = "実送信 (使い捨て pane に対してのみ)"
	} else {
		tickClient = simulateDryRunClient{inner: guarded, w: w, sent: &wouldSend}
	}
	_, _ = fmt.Fprintf(w, "\nmonitor.Tick を daemon と同じロジックで実行します (%s)\n", mode)

	pane, err := guarded.PaneGet(ctx, ownedPaneID)
	if err != nil {
		return fmt.Errorf("pane %s の取得に失敗: %w", ownedPaneID, err)
	}
	res.Agent, res.AgentStatus = pane.Agent, pane.AgentStatus
	_, _ = fmt.Fprintf(w, "  観測した pane: agent=%s agent_status=%s\n", pane.Agent, pane.AgentStatus)

	deps := monitor.Deps{Client: tickClient, Classifier: classifier, Config: cfg, Sleep: time.Sleep, Account: account}
	ps := &monitor.PaneState{}
	tick := func(n int, now time.Time) monitor.Outcome {
		outcome, tickErr := monitor.Tick(ctx, deps, pane, ps, rateState, now)
		report := TickReport{Now: now, Outcome: outcome.String(), WouldSend: wouldSend}
		wouldSend = nil
		_, _ = fmt.Fprintf(w, "  %d 回目の Tick → Outcome: %s\n", n, outcome)
		if tickErr != nil {
			report.Error = tickErr.Error()
			_, _ = fmt.Fprintf(w, "    エラー: %v\n", tickErr)
		}
		if outcome == monitor.OutcomeWaiting {
			until := ps.WaitUntil
			report.WaitUntil = &until
		}
		res.Ticks = append(res.Ticks, report)
		return outcome
	}

	if tick(1, time.Now()) != monitor.OutcomeWaiting {
		return nil
	}
	_, _ = fmt.Fprintf(w, "  待機状態に入りました (起床時刻 %s)\n", ps.WaitUntil.Format(time.RFC3339))

	synthNow := ps.WaitUntil.Add(time.Second)
	_, _ = fmt.Fprintf(w, "  2 回目の Tick は起床時刻を過ぎたと仮定して発火させます (実時間は待ちません, now=%s)\n", synthNow.Format(time.RFC3339))
	tick(2, synthNow)
	return nil
}

// scopedClient は paneClient を包み、ownedPaneID 以外の pane を対象にした呼び出しを
// 全て拒否する。simulate は自分が作った使い捨て pane 以外に絶対に触れてはいけないので、
// 「呼び出し側が正しい pane_id を渡す」という規律に頼るのではなく、型システムの外側で
// 機械的に強制できるガードとして実装する。pane を特定しない操作 (PaneList、Reachable) は
// paneClient に無いので、包む相手に出てこない。
type scopedClient struct {
	inner       paneClient
	ownedPaneID string
}

var errPaneNotOwned = errors.New("simulate が作成した pane 以外への操作を拒否しました")

func newScopedClient(inner paneClient, ownedPaneID string) *scopedClient {
	return &scopedClient{inner: inner, ownedPaneID: ownedPaneID}
}

func (c *scopedClient) guard(paneID string) error {
	if paneID != c.ownedPaneID {
		return fmt.Errorf("%w (要求された pane=%q, 所有 pane=%q)", errPaneNotOwned, paneID, c.ownedPaneID)
	}
	return nil
}

func (c *scopedClient) PaneGet(ctx context.Context, paneID string) (multiplexer.Pane, error) {
	if err := c.guard(paneID); err != nil {
		return multiplexer.Pane{}, err
	}
	return c.inner.PaneGet(ctx, paneID)
}

func (c *scopedClient) PaneRead(ctx context.Context, paneID string, opts multiplexer.PaneReadOptions) (string, error) {
	if err := c.guard(paneID); err != nil {
		return "", err
	}
	return c.inner.PaneRead(ctx, paneID, opts)
}

func (c *scopedClient) SendText(ctx context.Context, paneID, text string) error {
	if err := c.guard(paneID); err != nil {
		return err
	}
	return c.inner.SendText(ctx, paneID, text)
}

func (c *scopedClient) SendKeys(ctx context.Context, paneID string, keys ...string) error {
	if err := c.guard(paneID); err != nil {
		return err
	}
	return c.inner.SendKeys(ctx, paneID, keys...)
}

// simulateDryRunClient は daemon パッケージの dryRunClient と同じ考え方の decorator で、monitor.Tick に渡す
// monitor.Client (読む・送る) だけを包む。dryRunClient をそのまま使わない理由は、それが daemon のログファイル
// (*applog.Logger) 前提であるのに対し、simulate は「何を送るはずだったか」を
// そのままコマンドの標準出力 w に表示したいから。
type simulateDryRunClient struct {
	inner monitor.Client
	w     io.Writer
	// sent は送るはずだったものを積む先。nil なら積まない。
	sent *[]Sent
}

func (c simulateDryRunClient) PaneRead(ctx context.Context, paneID string, opts multiplexer.PaneReadOptions) (string, error) {
	return c.inner.PaneRead(ctx, paneID, opts)
}

func (c simulateDryRunClient) SendText(_ context.Context, paneID, text string) error {
	c.record(Sent{Text: text})
	_, _ = fmt.Fprintf(c.w, "  [dry-run] pane %s へ送るはずだったテキスト: %q\n", paneID, text)
	return nil
}

func (c simulateDryRunClient) SendKeys(_ context.Context, paneID string, keys ...string) error {
	c.record(Sent{Keys: keys})
	_, _ = fmt.Fprintf(c.w, "  [dry-run] pane %s へ送るはずだったキー: %s\n", paneID, strings.Join(keys, " "))
	return nil
}

func (c simulateDryRunClient) record(s Sent) {
	if c.sent != nil {
		*c.sent = append(*c.sent, s)
	}
}
