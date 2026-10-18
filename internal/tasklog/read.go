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

// ActiveTaskID はセッションの進行中のタスクのうち、最後に更新されたものの ID。無ければ空。
func (s Store) ActiveTaskID(d domain.Domain, sessionID string) string {
	if s.Dir == "" || !statefile.ValidSessionID(sessionID) {
		return ""
	}
	dir := filepath.Join(s.Dir, string(d), sessionID)
	var active Task
	for _, name := range taskFiles(dir) {
		t, ok := readTask(filepath.Join(dir, name))
		if ok && t.Status == "in_progress" && (active.ID == "" || t.UpdatedAt.After(active.UpdatedAt)) {
			active = t
		}
	}
	return active.ID
}

// Get は domain の側にあるセッションのタスク 1 つを返す。記録が無い、または ID がファイル名に使えなければ false。
func (s Store) Get(d domain.Domain, sessionID, taskID string) (Task, bool) {
	path, ok := s.taskPath(d, sessionID, taskID)
	if !ok {
		return Task{}, false
	}
	return readTask(path)
}

// Remove は domain の側にあるセッションのタスク 1 つの記録を消す。無ければ何もしない。
func (s Store) Remove(d domain.Domain, sessionID, taskID string) error {
	path, ok := s.taskPath(d, sessionID, taskID)
	if !ok {
		return fmt.Errorf("タスク %s/%s はファイル名に使えない", sessionID, taskID)
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("タスクの記録を消せない: %w", err)
	}
	return nil
}

func (s Store) taskPath(d domain.Domain, sessionID, taskID string) (string, bool) {
	if s.Dir == "" || !statefile.ValidSessionID(sessionID) || !statefile.ValidSessionID(taskID) {
		return "", false
	}
	return filepath.Join(s.Dir, string(d), sessionID, taskID+".json"), true
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
