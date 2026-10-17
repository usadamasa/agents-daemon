package sessionstate

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// sidecar file は揮発物として扱い、daemon が古いものを消す。保持期間を設定キーに
// せず定数に置くのは、config.json のキーを増やすほどの調整余地が無いため。
// 「この state がいつまで意味を持つか」は書き出し側の protocol の知識なので、
// 消す側 (daemon) ではなく読む側のここに置く。
const (
	// rateLimitRetention は利用上限 state を残す期間。resets_at は観測から高々
	// 5 時間先で、LatestWindow は未来の resets_at しか使わないため、24 時間より
	// 古い state が判定に効くことは無い。
	rateLimitRetention = 24 * time.Hour
	// contextRetention は context state を残す期間。block から session を逆引きする記録で、
	// 5 時間の上限待ちを跨いでも対応が残るよう利用上限より長く取る。
	contextRetention = 7 * 24 * time.Hour
)

// Prune は保持期間を過ぎた sidecar file を削除し、消した数を利用上限 state と context に
// 分けて返す。ディレクトリが空文字列 (ゼロ値の Store) または存在しない場合は何もしない。
func (s Store) Prune(now time.Time) (rateLimits, contexts int, err error) {
	rateLimits, rateErr := pruneDir(s.RateLimits, now, rateLimitRetention)
	contexts, contextErr := pruneDir(s.Context, now, contextRetention)
	return rateLimits, contexts, errors.Join(rateErr, contextErr)
}

// pruneDir は dir 直下の通常ファイルのうち、mtime が now から maxAge より古いものを
// 削除し、消した数を返す。
//
// 拡張子で絞らないのは、これらのディレクトリの中身が全てこの仕組みの書き出しで
// あり (json / md / 拡張子なしの marker / statusline の mktemp の残骸)、名前で
// 選り分ける意味が無いため。サブディレクトリは触らない。
//
// 判定に mtime を使うのは、中身 (observed_at) をパースせずに済ませるため。
// 壊れた JSON も同じ規則で消える。
func pruneDir(dir string, now time.Time, maxAge time.Duration) (int, error) {
	if dir == "" {
		return 0, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, fmt.Errorf("%s の走査に失敗: %w", dir, err)
	}

	cutoff := now.Add(-maxAge)
	removed := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return removed, fmt.Errorf("%s の情報を取得できません: %w", entry.Name(), err)
		}
		if !info.ModTime().Before(cutoff) {
			continue
		}
		if err := os.Remove(filepath.Join(dir, entry.Name())); err != nil {
			return removed, fmt.Errorf("%s の削除に失敗: %w", entry.Name(), err)
		}
		removed++
	}
	return removed, nil
}
