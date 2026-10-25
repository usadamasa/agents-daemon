package tasklog

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/usadamasa/agents-daemon/internal/statefile"
)

// ListTodos は全セッションの TodoWrite のリストを返す。壊れたファイルは読み飛ばす。
func (s Store) ListTodos() ([]TodoList, error) {
	if s.Dir == "" {
		return nil, nil
	}
	sessions, err := os.ReadDir(s.Dir)
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
		data, err := os.ReadFile(filepath.Join(s.Dir, session.Name(), todosFile)) // #nosec G304 -- tasks/ 配下の固定の名前
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
func (s Store) ActiveTaskID(sessionID string) string {
	if s.Dir == "" || !statefile.ValidSessionID(sessionID) {
		return ""
	}
	dir := filepath.Join(s.Dir, sessionID)
	var active Task
	for _, name := range taskFiles(dir) {
		t, ok := readTask(filepath.Join(dir, name))
		if ok && t.Status == "in_progress" && (active.ID == "" || t.UpdatedAt.After(active.UpdatedAt)) {
			active = t
		}
	}
	return active.ID
}

// Get はセッションのタスク 1 つを返す。記録が無い、または ID がファイル名に使えなければ false。
func (s Store) Get(sessionID, taskID string) (Task, bool) {
	path, ok := s.taskPath(sessionID, taskID)
	if !ok {
		return Task{}, false
	}
	return readTask(path)
}

// Remove はセッションのタスク 1 つの記録を消す。無ければ何もしない。
func (s Store) Remove(sessionID, taskID string) error {
	path, ok := s.taskPath(sessionID, taskID)
	if !ok {
		return fmt.Errorf("タスク %s/%s はファイル名に使えない", sessionID, taskID)
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("タスクの記録を消せない: %w", err)
	}
	return nil
}

func (s Store) taskPath(sessionID, taskID string) (string, bool) {
	if s.Dir == "" || !statefile.ValidSessionID(sessionID) || !statefile.ValidSessionID(taskID) {
		return "", false
	}
	return filepath.Join(s.Dir, sessionID, taskID+".json"), true
}
