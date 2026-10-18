package parking

import (
	"errors"
	"os"
	"path/filepath"
	"time"
)

// Prune は保持期間を過ぎたメモを mtime で消し、消した数を返す。open は OpenRetention、handed /
// discarded と読めないメモ、消し損ねた一時ファイルは ClosedRetention で消す。ロックのファイルは残す。
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
		dir := filepath.Join(s.Dir, d.Name())
		entries, err := os.ReadDir(dir)
		if err != nil {
			errs = append(errs, err)
			continue
		}
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
