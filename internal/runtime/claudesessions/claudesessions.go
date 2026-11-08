// Package claudesessions は Claude Code のセッションの登録簿 ($CLAUDE_CONFIG_DIR/sessions/<pid>.json) を読む。
// 動いているセッションごとに Claude Code が書くファイルで、ListAgents と SendMessage が宛先に使う名前と、
// process の pid を持つ。statusline の session_name は会話の題で、名前を付けずに起動したセッションではこの名前と
// 一致しない。書くのは Claude Code で、daemon は読むだけ。
//
// cc-premise: 2.1.316 peer-messaging sessions/<pid>.json の sessionId と name が ListAgents の名前の対 (nameSource は derived か user)
// cc-premise: 2.1.319 peer-messaging sessions/<pid>.json の pid は claude の process の pid (rex の foreground の pid と一致するのを実測)
package claudesessions

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/usadamasa/agents-daemon/internal/multiplexer"
)

// Store は登録簿の置き場所を持つ。ゼロ値は Dir が空で、読み込みは空になる。
type Store struct {
	Dir string
}

// New は Claude Code の設定ディレクトリ (apppath.ClaudeConfigDir) から置き場所を導出する。
func New(configDir string) Store {
	return Store{Dir: filepath.Join(configDir, "sessions")}
}

// entry は登録簿の 1 ファイルのうち読む欄。
type entry struct {
	PID       int    `json:"pid"`
	SessionID string `json:"sessionId"`
	Name      string `json:"name"`
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
