package harvest

import (
	"encoding/json"
	"path/filepath"
	"time"

	"github.com/usadamasa/agents-daemon/internal/statefile"
)

// HarvestState は --incremental モードのための永続状態。
type HarvestState struct {
	LastRunTS string `json:"last_run_ts"`
}

const stateFileName = "harvest-state.json"

// loadState は out ディレクトリの harvest-state.json を読む。
// ファイルが無い場合は空 state を返す (エラーではない)。
func loadState(outDir string) (*HarvestState, error) {
	var s HarvestState
	if _, err := statefile.ReadJSON(filepath.Join(outDir, stateFileName), &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// saveState は out ディレクトリに harvest-state.json を原子的に書く (statefile)。
// out ディレクトリが無ければ作る (初回実行時に備える)。
func saveState(outDir string, s *HarvestState) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return statefile.WriteFileAtomic(filepath.Join(outDir, stateFileName), "."+stateFileName+".*", data)
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
