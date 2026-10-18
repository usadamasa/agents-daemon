package tasklog

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/usadamasa/agents-daemon/internal/domain"
	"github.com/usadamasa/agents-daemon/internal/statefile"
)

// ListTodos は domain の全セッションの TodoWrite のリストを返す。壊れたファイルは読み飛ばす。
func (s Store) ListTodos(d domain.Domain) ([]TodoList, error) {
	if s.Dir == "" {
		return nil, nil
	}
	root := filepath.Join(s.Dir, string(d))
	sessions, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("tasks の走査に失敗: %w", err)
	}
	var lists []TodoList
	for _, session := range sessions {
		if !session.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, session.Name(), todosFile)) // #nosec G304 -- tasks/ 配下の固定の名前
		if err != nil {
			continue
		}
		var list TodoList
		if json.Unmarshal(data, &list) != nil || list.SessionID == "" {
			continue
		}
		lists = append(lists, list)
	}
	return lists, nil
}

// HasSession は domain の側にそのセッションの記録 (ディレクトリ) があるかを返す。ボードが、業務に倒した
// セッションを cwd だけで私用の側に描かないために使う。
func (s Store) HasSession(d domain.Domain, sessionID string) bool {
	if s.Dir == "" || !statefile.ValidSessionID(sessionID) {
		return false
	}
	info, err := os.Stat(filepath.Join(s.Dir, string(d), sessionID))
	return err == nil && info.IsDir()
}
