package tasklog

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/usadamasa/agents-daemon/internal/statefile"
)

// Prune は保持期間を過ぎた記録を mtime で消し、空になったセッションのディレクトリも消す。消した
// ファイルの数を返す。完了したタスクは CompletedRetention、それ以外のタスクと TodoWrite のリスト、
// 消し損ねた一時ファイルは Retention で消す。keepCompleted が true を返すセッションの完了タスクは消さない。
// nil はどのセッションも残さない。
func (s Store) Prune(now time.Time, keepCompleted func(sessionID string) bool) (int, error) {
	if s.Dir == "" {
		return 0, nil
	}
	sessions, err := os.ReadDir(s.Dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, err
	}
	removed := 0
	var errs []error
	for _, session := range sessions {
		if !session.IsDir() {
			continue
		}
		keep := keepCompleted != nil && keepCompleted(session.Name())
		n, err := pruneSession(filepath.Join(s.Dir, session.Name()), now, keep)
		removed += n
		if err != nil {
			errs = append(errs, err)
		}
	}
	return removed, errors.Join(errs...)
}

// Touch は生きているセッションの未完了のタスクと TodoWrite のリストの mtime を now に進める。中身は変えない。完了したタスクは
// 進めず、CompletedRetention で消えるままにする。ファイル名に使えない ID は飛ばして残りを進め、エラーにまとめて返す。
func (s Store) Touch(sessionIDs []string, now time.Time) error {
	if s.Dir == "" {
		return nil
	}
	var errs []error
	for _, id := range sessionIDs {
		if !statefile.ValidSessionID(id) {
			errs = append(errs, fmt.Errorf("session ID %q はファイル名に使えない", id))
			continue
		}
		dir := filepath.Join(s.Dir, id)
		paths := []string{filepath.Join(dir, todosFile)}
		for _, name := range taskFiles(dir) {
			if t, ok := readTask(filepath.Join(dir, name)); ok && t.Status != "completed" {
				paths = append(paths, filepath.Join(dir, name))
			}
		}
		for _, path := range paths {
			if err := os.Chtimes(path, now, now); err != nil && !errors.Is(err, os.ErrNotExist) {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

// pruneSession はセッション 1 つぶんのディレクトリを掃除する。
func pruneSession(dir string, now time.Time, keepCompleted bool) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, err
	}
	removed := 0
	var errs []error
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		path := filepath.Join(dir, e.Name())
		info, err := e.Info()
		if err != nil {
			continue
		}
		if !expired(path, e.Name(), now.Sub(info.ModTime()), keepCompleted) {
			continue
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, err)
			continue
		}
		removed++
	}
	// 空なら消す。中身が残っていれば Remove は失敗するので、その失敗は無視する。
	_ = os.Remove(dir)
	return removed, errors.Join(errs...)
}

// expired は age 経ったファイルが保持期間を過ぎたかを返す。
func expired(path, name string, age time.Duration, keepCompleted bool) bool {
	if name != todosFile && filepath.Ext(name) == ".json" && name[0] != '.' {
		if t, ok := readTask(path); ok && t.Status == "completed" {
			return !keepCompleted && age >= CompletedRetention
		}
	}
	return age >= Retention
}
