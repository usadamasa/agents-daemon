package statefile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// removeFile はテストが Remove の失敗と並行削除を差し込む継ぎ目。
var removeFile = os.Remove

// PruneDir は dir 直下の通常ファイルのうち mtime が cutoff 以前のものを消し、消した数を返す。
// dir が空文字列 (ゼロ値の Store) または存在しない場合は何もしない。サブディレクトリは触らない。
//
// 拡張子で絞らないのは、置き場の中身が全て書き手の書き出し (と消し損ねた一時ファイル) で、
// 名前で選り分ける意味が無いため。判定に mtime を使うのは中身をパースせずに済ませるためで、
// 壊れたファイルも同じ規則で消える。保持期間は書式の知識なので、cutoff は呼び出し側が
// 各書式の定数から求める。
//
// 走査のあとに別プロセスが消したファイル (ErrNotExist) は数えずに飛ばす。それ以外の失敗では
// そこで止め、それまでに消した数を返す。
func PruneDir(dir string, cutoff time.Time) (int, error) {
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
	removed := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return removed, fmt.Errorf("%s の情報を取得できません: %w", entry.Name(), err)
		}
		if info.ModTime().After(cutoff) {
			continue
		}
		if err := removeFile(filepath.Join(dir, entry.Name())); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return removed, fmt.Errorf("%s の削除に失敗: %w", entry.Name(), err)
		}
		removed++
	}
	return removed, nil
}
