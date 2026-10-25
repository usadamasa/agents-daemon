package parking

import (
	"errors"
	"os"
	"path/filepath"
	"time"
)

// Prune は保持期間を過ぎたメモを mtime で消し、消した数を返す。open は OpenRetention、handed /
// discarded と読めないメモ、消し損ねた一時ファイルは ClosedRetention で消す。ロックのファイルは残す。
// Set と同じロックを取る。Set が読んでから書くまでの間に消すと、その書き込みがメモを作り直す。
func (s Store) Prune(now time.Time) (int, error) {
	if s.Dir == "" {
		return 0, nil
	}
	if _, err := os.Stat(s.Dir); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, err
	}
	removed := 0
	err := s.locked(func(dir string) error {
		n, err := pruneDir(dir, now)
		removed = n
		return err
	})
	return removed, err
}

// pruneDir は dir の保持期間を過ぎたメモを消し、消した数を返す。呼び出し側がロックを持つ。
func pruneDir(dir string, now time.Time) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, err
	}
	removed := 0
	var errs []error
	for _, e := range entries {
		if e.IsDir() || e.Name() == lockFile {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		path := filepath.Join(dir, e.Name())
		if !expired(path, e, now.Sub(info.ModTime())) {
			continue
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, err)
			continue
		}
		removed++
	}
	return removed, errors.Join(errs...)
}

// expired は age 経ったファイルが保持期間を過ぎたかを返す。
func expired(path string, e os.DirEntry, age time.Duration) bool {
	if age >= OpenRetention {
		return true
	}
	if age < ClosedRetention {
		return false
	}
	if !isItemFile(e) {
		return true
	}
	item, ok := readItem(path)
	return !ok || item.Status != StatusOpen
}
