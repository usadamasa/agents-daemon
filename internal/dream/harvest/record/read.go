package record

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/usadamasa/agents-daemon/internal/dream/jsonlscan"
)

// LoadForProject は harvest ディレクトリ配下の全 jsonl から、
// cwd_project が指定 canonical と一致するセッションだけを読み込む。
// ディレクトリが存在しない場合は空スライスと nil error を返す (未 harvest 状態を許容)。
func LoadForProject(harvestDir, project string) ([]Record, error) {
	var out []Record
	err := filepath.WalkDir(harvestDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return fs.SkipAll
			}
			return err
		}
		if d.IsDir() {
			return nil
		}
		if filepath.Ext(path) != ".jsonl" {
			return nil
		}
		recs, err := readProjectFile(path, project)
		if err != nil {
			return err
		}
		out = append(out, recs...)
		return nil
	})
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	return out, nil
}

// readProjectFile は 1 つの harvest jsonl から project 一致 record を返す。
func readProjectFile(path, project string) ([]Record, error) {
	f, err := os.Open(path) // #nosec G304 -- CLI: パスは WalkDir 由来
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var out []Record
	scanner := jsonlscan.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var rec Record
		if err := json.Unmarshal(line, &rec); err != nil {
			continue
		}
		if rec.CWDProject != project {
			continue
		}
		out = append(out, rec)
	}
	return out, scanner.Err()
}
