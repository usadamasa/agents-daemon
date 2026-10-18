package tasklog

import (
	"errors"
	"os"
	"path/filepath"
	"time"
)

// Prune は保持期間を過ぎた記録を mtime で消し、空になったセッションのディレクトリも消す。消した
// ファイルの数を返す。完了したタスクは CompletedRetention、それ以外のタスクと TodoWrite のリスト、
// 消し損ねた一時ファイルは Retention で消す。
func (s Store) Prune(now time.Time) (int, error) {
	if s.Dir == "" {
		return 0, nil
	}
	domains, err := os.ReadDir(s.Dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, err
	}
	removed := 0
	var errs []error
	for _, d := range domains {
		if !d.IsDir() {
			continue
		}
		root := filepath.Join(s.Dir, d.Name())
		sessions, err := os.ReadDir(root)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		for _, session := range sessions {
			if !session.IsDir() {
				continue
			}
			n, err := pruneSession(filepath.Join(root, session.Name()), now)
			removed += n
			if err != nil {
				errs = append(errs, err)
			}
		}
	}
	return removed, errors.Join(errs...)
}

// pruneSession はセッション 1 つぶんのディレクトリを掃除する。
func pruneSession(dir string, now time.Time) (int, error) {
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
		if !expired(path, e.Name(), now.Sub(info.ModTime())) {
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
func expired(path, name string, age time.Duration) bool {
	if age >= Retention {
		return true
	}
	if name == todosFile || filepath.Ext(name) != ".json" || name[0] == '.' {
		return false
	}
	t, ok := readTask(path)
	return ok && t.Status == "completed" && age >= CompletedRetention
}
