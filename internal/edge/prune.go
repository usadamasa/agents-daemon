package edge

import (
	"errors"
	"os"
	"path/filepath"
	"time"
)

// Prune は Retention のあいだ更新の無いファイル (辺と消し損ねた一時ファイル) を mtime で消し、消した数を返す。
// ロックのファイルは残す (消すと、開いたままの別のプロセスと別のロックを取り合うことになる)。Add と同じロックを
// 取り、Add が読んでから書くまでの間には消さない。
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
		entries, err := os.ReadDir(dir)
		if err != nil {
			return err
		}
		var errs []error
		for _, e := range entries {
			if e.IsDir() || e.Name() == lockFile {
				continue
			}
			info, err := e.Info()
			if err != nil || now.Sub(info.ModTime()) < Retention {
				continue
			}
			if err := os.Remove(filepath.Join(dir, e.Name())); err != nil && !errors.Is(err, os.ErrNotExist) {
				errs = append(errs, err)
				continue
			}
			removed++
		}
		return errors.Join(errs...)
	})
	return removed, err
}
