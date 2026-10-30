package harvest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// contentBlock は message.content[] の要素を表す。
type contentBlock struct {
	Type  string          `json:"type"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

// walkOptions は walkJSONLFiles の走査オプション。
type walkOptions struct {
	Days int
}

// walkJSONLFiles は Claude Code のセッション JSONL (~/.claude/projects 配下) を走査し、
// 各ファイルに対してコールバック関数を呼び出す。
// ディレクトリが存在しない場合はエラーなしで終了する。
func walkJSONLFiles(dir string, opts walkOptions, fn func(path string) error) error {
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return nil
	}

	cutoff := time.Now().Add(-time.Duration(opts.Days) * 24 * time.Hour)

	return filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".jsonl") {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return nil
		}
		if info.ModTime().Before(cutoff) {
			return nil
		}

		return fn(path)
	})
}
