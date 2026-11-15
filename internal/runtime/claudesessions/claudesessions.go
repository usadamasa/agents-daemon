// Package claudesessions は Claude Code のセッションの登録簿 ($CLAUDE_CONFIG_DIR/sessions/<pid>.json) を読む。
// 動いているセッションごとに Claude Code が書くファイルで、ListAgents と SendMessage が宛先に使う名前と、
// process の pid を持つ。statusline の session_name は会話の題で、名前を付けずに起動したセッションではこの名前と
// 一致しない。書くのは Claude Code で、daemon は読むだけ。
//
// cc-premise: 2.1.316 peer-messaging sessions/<pid>.json の sessionId と name が ListAgents の名前の対 (nameSource は derived か user)
// cc-premise: 2.1.319 peer-messaging sessions/<pid>.json の pid は claude の process の pid (rex の foreground の pid と一致するのを実測)
// cc-premise: 2.1.325 desktop sessions/<pid>.json の entrypoint は端末起動が cli、Desktop 由来が claude-desktop (Desktop が抱える 2.1.317 で実測)
//
// Desktop 由来のセッションを rex で引き継ぐ (#130) のにも使う。`claude agents --json` は動いている interactive の
// セッションを出さない (2.1.325 で実測) ので、Desktop 由来の列挙はこの登録簿に頼る。
package claudesessions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"syscall"
	"time"

	"github.com/usadamasa/agents-daemon/internal/multiplexer"
)

// Store は登録簿の置き場所を持つ。ゼロ値は Dir が空で、読み込みは空になる。
type Store struct {
	Dir string
	// Alive は process の生存の確かめ。nil なら ProcessAlive。テストが差し替える。
	Alive func(pid int) bool
}

// New は Claude Code の設定ディレクトリ (apppath.ClaudeConfigDir) から置き場所を導出する。
func New(configDir string) Store {
	return Store{Dir: filepath.Join(configDir, "sessions")}
}

// EntrypointDesktop は Claude Code Desktop が起こしたセッションの entrypoint。端末から起動したものは cli。
const EntrypointDesktop = "claude-desktop"

// entry は登録簿の 1 ファイルのうち読む欄。
type entry struct {
	PID        int    `json:"pid"`
	SessionID  string `json:"sessionId"`
	Name       string `json:"name"`
	Cwd        string `json:"cwd"`
	Entrypoint string `json:"entrypoint"`
	Status     string `json:"status"`
}

// Session は登録簿に載ったセッション。Desktop 由来の一覧 (Desktop) と引き継ぎの確かめ (Find) が返す。
type Session struct {
	PID        int    `json:"pid"`
	SessionID  string `json:"session_id"`
	Name       string `json:"name,omitempty"`
	Cwd        string `json:"cwd,omitempty"`
	Entrypoint string `json:"entrypoint,omitempty"`
	// Status は Claude Code が書く idle / busy。
	Status string `json:"status,omitempty"`
}

func (x entry) session() Session {
	return Session{PID: x.PID, SessionID: x.SessionID, Name: x.Name, Cwd: x.Cwd, Entrypoint: x.Entrypoint, Status: x.Status}
}

// Desktop は Desktop 由来 (entrypoint=claude-desktop) で、process が生きているセッションを session ID の順に返す。
// 終わったセッションのファイルが残っていても、process が居なければ載せない。
func (s Store) Desktop() ([]Session, error) {
	entries, err := s.readAll()
	if err != nil {
		return nil, err
	}
	var out []Session
	for _, x := range entries {
		if x.Entrypoint != EntrypointDesktop || x.SessionID == "" || !s.alive(x.PID) {
			continue
		}
		out = append(out, x.session())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SessionID < out[j].SessionID })
	return out, nil
}

// Find は sessionID のセッションのうち process が生きているものを返す。無ければ ok=false。
// 同じ session ID のファイルが複数残っていても (pid を変えて resume した)、生きているものだけを見る。
func (s Store) Find(sessionID string) (Session, bool, error) {
	entries, err := s.readAll()
	if err != nil {
		return Session{}, false, err
	}
	for _, x := range entries {
		if x.SessionID == sessionID && s.alive(x.PID) {
			return x.session(), true, nil
		}
	}
	return Session{}, false, nil
}

// WaitClosed は sessionID のセッションの process が居なくなるまで interval ごとに登録簿を引いて待つ。
// 利用者が Desktop でセッションを閉じるのを待ってから rex で resume するのに使う。ctx が切れたら ctx の error。
func (s Store) WaitClosed(ctx context.Context, sessionID string, interval time.Duration) error {
	for {
		_, open, err := s.Find(sessionID)
		if err != nil {
			return err
		}
		if !open {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}
	}
}

// alive は pid の process が居るか。Alive が nil なら ProcessAlive。
func (s Store) alive(pid int) bool {
	if s.Alive != nil {
		return s.Alive(pid)
	}
	return ProcessAlive(pid)
}

// ProcessAlive は pid の process が居るかを signal 0 で確かめる。権限が無い (EPERM) のは居るとみなす。
// pid が使い回されていれば別の process を居ると読むが、登録簿の session ID と組で引くので害は小さい。
// sandbox の中では EPERM になり全部を居ると読むので、daemon のバイナリは sandbox の外で打つ。
func ProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// Names は session ID → ListAgents の名前を返す。名前か session ID の無いものは載せない。
// 終了したセッションのファイルが残っていても、引くのは見えている pane の session ID だけなので害は無い。
func (s Store) Names() (map[string]string, error) {
	entries, err := s.readAll()
	if err != nil {
		return nil, err
	}
	names := map[string]string{}
	for _, x := range entries {
		if x.SessionID != "" && x.Name != "" {
			names[x.SessionID] = x.Name
		}
	}
	return names, nil
}

// SessionIDs は process の pid → session ID を返す。pid か session ID の無いものは載せない。
// daemon が、statusline の記録 (context/) から session の引けない pane の session を rex の foreground の pid で補うのに使う。
// 終了したセッションのファイルが残っていても、pid は使い回されるまで一致しない。
func (s Store) SessionIDs() (map[int]string, error) {
	entries, err := s.readAll()
	if err != nil {
		return nil, err
	}
	ids := map[int]string{}
	for _, x := range entries {
		if x.PID != 0 && x.SessionID != "" {
			ids[x.PID] = x.SessionID
		}
	}
	return ids, nil
}

// Fill は SessionID の無い Claude の pane に、前景の pid で登録簿から session ID を補う (無ければそのまま)。
// 登録簿を読むのは補う pane があるときだけ (tick ごとに sessions/ を走査しない)。
func (s Store) Fill(panes []multiplexer.Pane) error {
	var ids map[int]string
	for i := range panes {
		pane := &panes[i]
		if pane.SessionID != "" || pane.PID == 0 || !multiplexer.IsClaudeAgent(*pane) {
			continue
		}
		if ids == nil {
			var err error
			if ids, err = s.SessionIDs(); err != nil {
				return err
			}
		}
		pane.SessionID = ids[pane.PID]
	}
	return nil
}

// readAll は登録簿の .json を全部読む。壊れたものは読み飛ばす。ディレクトリが無い・ゼロ値の Store なら空。
func (s Store) readAll() ([]entry, error) {
	if s.Dir == "" {
		return nil, nil
	}
	files, err := os.ReadDir(s.Dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("セッションの登録簿 (%s) の走査に失敗: %w", s.Dir, err)
	}
	var entries []entry
	for _, e := range files {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.Dir, e.Name())) // #nosec G304 -- 登録簿のディレクトリ直下の名前
		if err != nil {
			continue
		}
		var x entry
		if json.Unmarshal(data, &x) != nil {
			continue
		}
		entries = append(entries, x)
	}
	return entries, nil
}
