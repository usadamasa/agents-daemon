package tasklog

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/usadamasa/agents-daemon/internal/statefile"
	"github.com/usadamasa/agents-daemon/internal/tasklog/record"
)

// ListTodos は全セッションの TodoWrite のリストを返す。壊れたファイルは読み飛ばす。
func (s Store) ListTodos() ([]record.TodoList, error) {
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
	var lists []record.TodoList
	for _, session := range sessions {
		if !session.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.Dir, session.Name(), todosFile)) // #nosec G304 -- tasks/ 配下の固定の名前
		if err != nil {
			continue
		}
		var list record.TodoList
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
	var active record.Task
	for _, name := range taskFiles(dir) {
		t, ok := readTask(filepath.Join(dir, name))
		if ok && t.Status == "in_progress" && (active.ID == "" || t.UpdatedAt.After(active.UpdatedAt)) {
			active = t
		}
	}
	return active.ID
}

// Get はセッションのタスク 1 つを返す。記録が無い、または ID がファイル名に使えなければ false。
func (s Store) Get(sessionID, taskID string) (record.Task, bool) {
	path, ok := s.taskPath(sessionID, taskID)
	if !ok {
		return record.Task{}, false
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

// Dismiss は終了したセッションのタスク 1 つの記録を、完了しているかを問わず消す (依存図の Dismiss、#337)。
// exited はセッションが記録の時刻より後に終了したと確かめる (daemonstate.SessionExited)。確かめられなければ消さない。
// 生きているセッションの記録は次の TaskList で戻るうえ、未完了のものを消すと LiveTaskKept を破るため。
func (s Store) Dismiss(sessionID, taskID string, exited func(sessionID string, after time.Time) error) error {
	t, ok := s.Get(sessionID, taskID)
	if !ok {
		return fmt.Errorf("タスク %s/%s が無い", sessionID, taskID)
	}
	if err := exited(sessionID, t.UpdatedAt); err != nil {
		return fmt.Errorf("タスク %s/%s は消さない: %w", sessionID, taskID, err)
	}
	return s.Remove(sessionID, taskID)
}

func (s Store) taskPath(sessionID, taskID string) (string, bool) {
	if s.Dir == "" || !statefile.ValidSessionID(sessionID) || !statefile.ValidSessionID(taskID) {
		return "", false
	}
	return filepath.Join(s.Dir, sessionID, taskID+".json"), true
}
