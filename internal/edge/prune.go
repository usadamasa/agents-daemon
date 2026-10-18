package edge

import (
	"errors"
	"os"
	"path/filepath"
	"time"
)

// Prune は Retention のあいだ更新の無いファイル (辺と消し損ねた一時ファイル) を mtime で消し、消した数を返す。
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
		n, err := pruneDir(filepath.Join(s.Dir, d.Name()), now)
		removed += n
		errs = append(errs, err)
	}
	return removed, errors.Join(errs...)
}

// pruneDir は domain のディレクトリ 1 つから期限切れのファイルを消す。
func pruneDir(dir string, now time.Time) (int, error) {
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
	return removed, errors.Join(errs...)
}
