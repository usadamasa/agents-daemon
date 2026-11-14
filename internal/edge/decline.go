package edge

import (
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/usadamasa/agents-daemon/internal/ref"
)

// declineExt は人が消した sid:S → pr:P の組を「導かない」と記録するファイルの拡張子 (#576)。辺と同じ edges/ に
// <辺の id>.declined で置く。拡張子が .json でないので辺の読み手 (readAll) には見えず、Prune が辺と同じ
// Retention で mtime から消す。手順の性質は models/prlink の NoRederive が持つ。
const declineExt = ".declined"

func declinePath(dir, before, after string) string {
	return filepath.Join(dir, edgeID(before, after)+declineExt)
}

// declinable は組が「導かない」を記録する形 (sid:S → pr:P、Anchor が導く形) か。
func declinable(before, after string) bool {
	_, isSID := ref.ParseSessionID(before)
	it, isItem := ref.ParseItem(after)
	return isSID && isItem && it.Kind == "pr"
}

// decline は組を「導かない」と記録する。中身は人が読むための組だけで、読み手は有無と mtime しか見ない。
// 呼び出し側がロックを持つ。
func decline(dir, before, after string) error {
	return os.WriteFile(declinePath(dir, before, after), []byte(before+" "+after+"\n"), 0o600)
}

// undecline は組の記録を消す。呼び出し側がロックを持つ。
func undecline(dir, before, after string) error {
	if err := os.Remove(declinePath(dir, before, after)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// declined は組の記録があれば mtime を now に進めて true を返す。導きを飛ばすたびに保持期間を数え直し、導く条件が
// 揃っている間は Prune に消させない (models/prlink の TouchDecline)。呼び出し側がロックを持つ。
func declined(dir, before, after string, now time.Time) (bool, error) {
	path := declinePath(dir, before, after)
	if err := os.Chtimes(path, now, now); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return true, err
	}
	return true, nil
}
