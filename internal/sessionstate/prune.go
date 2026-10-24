package sessionstate

import (
	"errors"
	"time"

	"github.com/usadamasa/agents-daemon/internal/ratelimit"
	"github.com/usadamasa/agents-daemon/internal/sessioncontext"
	"github.com/usadamasa/agents-daemon/internal/statefile"
)

// Prune は保持期間を過ぎた sidecar file を削除し、消した数を利用上限 state と context に
// 分けて返す。ディレクトリが空文字列 (ゼロ値の Store) または存在しない場合は何もしない。
//
// sidecar file は揮発物として扱い、daemon が古いものを消す。保持期間は書式の知識なので
// 各書式のパッケージの定数 (ratelimit.Retention、sessioncontext.Retention) を使う。
// 中身は json / md / 拡張子なしの marker / statusline の mktemp の残骸で、名前で絞らない。
func (s Store) Prune(now time.Time) (rateLimits, contexts int, err error) {
	rateLimits, rateErr := statefile.PruneDir(s.RateLimits.Dir, now.Add(-ratelimit.Retention))
	contexts, contextErr := statefile.PruneDir(s.Context.Dir, now.Add(-sessioncontext.Retention))
	return rateLimits, contexts, errors.Join(rateErr, contextErr)
}
