package harvest

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// HarvestState は --incremental モードのための永続状態。
type HarvestState struct {
	LastRunTS string `json:"last_run_ts"`
}

const stateFileName = "harvest-state.json"

// loadState は out ディレクトリの harvest-state.json を読む。
// ファイルが無い場合は空 state を返す (エラーではない)。
func loadState(outDir string) (*HarvestState, error) {
	path := filepath.Join(outDir, stateFileName)
	data, err := os.ReadFile(path) // #nosec G304 -- CLI: パスは out ディレクトリ固定
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &HarvestState{}, nil
		}
		return nil, err
	}
	var s HarvestState
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// saveState は out ディレクトリに harvest-state.json を書く。
// mkdir -p も行う (初回実行時に out ディレクトリが無い場合に備える)。
func saveState(outDir string, s *HarvestState) error {
	if err := os.MkdirAll(outDir, 0o750); err != nil {
		return err
	}
	path := filepath.Join(outDir, stateFileName)
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644) // #nosec G306 -- ユーザー個人用状態ファイル
}

// parseLastRun は state の LastRunTS を time.Time にする。未設定・パース失敗時は
// ゼロ値 (= "常に処理する" 相当) を返す。
func parseLastRun(s *HarvestState) time.Time {
	if s == nil || s.LastRunTS == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s.LastRunTS)
	if err != nil {
		return time.Time{}
	}
	return t
}
