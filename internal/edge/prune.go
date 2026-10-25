package edge

import (
	"time"

	"github.com/usadamasa/agents-daemon/internal/statefile"
)

// Prune は Retention のあいだ更新の無いファイル (辺と消し損ねた一時ファイル) を mtime で消し、消した数を返す。
func (s Store) Prune(now time.Time) (int, error) {
	return statefile.PruneDir(s.Dir, now.Add(-Retention))
}
