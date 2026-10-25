package candidates

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/usadamasa/agents-daemon/internal/dream"
)

// Read は candidates.jsonl を読み Candidate slice を返す。
// ファイル未存在は「候補なし」として空 slice + nil error を返す (Stage が呼ぶ挙動)。
// 各行の JSON decode 失敗はスキップし、scan エラーのみ error として返す。
func Read(path string) ([]Candidate, error) {
	f, err := os.Open(path) // #nosec G304 -- CLI: 明示指定 or auto-resolve path
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var out []Candidate
	scanner := dream.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var c Candidate
		if err := json.Unmarshal(line, &c); err != nil {
			continue
		}
		out = append(out, c)
	}
	return out, scanner.Err()
}

// FindByID は candidates.jsonl から指定 ID の 1 レコードを返す。
// 見つからなければエラー。Adopt が呼ぶ。
//
// candidates.jsonl は通常 100 候補以下の小規模ファイルのため、全件読み込んで
// 線形検索する。パフォーマンスクリティカルなら bytes.Contains prefilter を検討する。
func FindByID(path, id string) (*Candidate, error) {
	cands, err := Read(path)
	if err != nil {
		return nil, err
	}
	for i := range cands {
		if cands[i].ID == id {
			return &cands[i], nil
		}
	}
	return nil, fmt.Errorf("candidate %q not found in %s", id, path)
}

// Write は cands を outFile に jsonl 形式で書き出す。
// 親ディレクトリは自動作成する。出力は 1 行 1 candidate。
// HTML エスケープは無効化 (URL や記号を可読に保つ)。Mine が呼ぶ。
func Write(outFile string, cands []Candidate) error {
	if err := os.MkdirAll(filepath.Dir(outFile), 0o750); err != nil {
		return err
	}
	f, err := os.Create(outFile) // #nosec G304 -- CLI: 明示指定 out path
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	enc := json.NewEncoder(f)
	enc.SetEscapeHTML(false)
	for _, c := range cands {
		if err := enc.Encode(c); err != nil {
			return err
		}
	}
	return nil
}

// CountByKind は Candidate slice を Kind* ごとに集計する。
// Stage report と mine verbose 出力の両方で使う。
func CountByKind(cands []Candidate) (skillBody, memoryDedup, unhomed int) {
	for _, c := range cands {
		switch c.Kind {
		case KindSkillBody:
			skillBody++
		case KindMemoryDedup:
			memoryDedup++
		case KindUnhomed:
			unhomed++
		}
	}
	return
}
