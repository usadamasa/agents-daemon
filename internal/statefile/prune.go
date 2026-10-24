package statefile

import (
	"os"
	"time"
)

// removeFile はテストが Remove の失敗と並行削除を差し込む継ぎ目。
var removeFile = os.Remove

// PruneDir は未実装。
func PruneDir(dir string, cutoff time.Time) (int, error) {
	return 0, nil
}
