// Package claudesessions は Claude Code のセッションの登録簿 ($CLAUDE_CONFIG_DIR/sessions/<pid>.json) を読む。
// 動いているセッションごとに Claude Code が書くファイルで、ListAgents と SendMessage が宛先に使う名前を持つ。
// statusline の session_name は会話の題で、名前を付けずに起動したセッションではこの名前と一致しない。
// 書くのは Claude Code で、daemon は読むだけ。
//
// cc-premise: 2.1.316 peer-messaging sessions/<pid>.json の sessionId と name が ListAgents の名前の対 (nameSource は derived か user)
package claudesessions

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
	SessionID string `json:"sessionId"`
	Name      string `json:"name"`
}

// Names は session ID → ListAgents の名前を返す。名前か session ID の無いもの、壊れたものは読み飛ばす。
// 終了したセッションのファイルが残っていても、引くのは見えている pane の session ID だけなので害は無い。
func (s Store) Names() (map[string]string, error) {
	names := map[string]string{}
	if s.Dir == "" {
		return names, nil
	}
	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return names, nil
		}
		return nil, fmt.Errorf("セッションの登録簿 (%s) の走査に失敗: %w", s.Dir, err)
	}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.Dir, e.Name())) // #nosec G304 -- 登録簿のディレクトリ直下の名前
		if err != nil {
			continue
		}
		var x entry
		if json.Unmarshal(data, &x) != nil || x.SessionID == "" || x.Name == "" {
			continue
		}
		names[x.SessionID] = x.Name
	}
	return names, nil
}
