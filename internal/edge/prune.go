package edge

import (
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/usadamasa/agents-daemon/internal/statefile"
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
		n, err := statefile.PruneDir(filepath.Join(s.Dir, d.Name()), now.Add(-Retention))
		removed += n
		errs = append(errs, err)
	}
	return removed, errors.Join(errs...)
}
