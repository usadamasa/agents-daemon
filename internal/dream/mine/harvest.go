package mine

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/usadamasa/agents-daemon/internal/dream/jsonlscan"
)

// HarvestSession は harvest 出力 (harvest jsonl) の 1 レコード。
// Mine が必要とするフィールドだけ抜粋する (前方互換 - unknown フィールドは無視)。
type HarvestSession struct {
	SessionID         string           `json:"session_id"`
	TSStart           string           `json:"ts_start,omitempty"`
	TSEnd             string           `json:"ts_end,omitempty"`
	CWD               string           `json:"cwd,omitempty"`
	CWDProject        string           `json:"cwd_project,omitempty"`
	NMessages         int              `json:"n_messages"`
	SkillsInvoked     []string         `json:"skills_invoked"`
	FeedbackSignals   []MinedFeedback  `json:"feedback_signals"`
	FailedInvocations []MinedFailedInv `json:"failed_invocations"`
}

// MinedFeedback は harvest の FeedbackSignal と互換の subset。
type MinedFeedback struct {
	Quote string `json:"quote"`
	Kind  string `json:"kind"`
}

// MinedFailedInv は harvest の FailedInvocation と互換の subset。
type MinedFailedInv struct {
	InvokedSkill string `json:"invoked_skill"`
	UserReaction string `json:"user_reaction"`
}

// loadHarvestForProject は harvest ディレクトリ配下の全 jsonl から、
// cwd_project が指定 canonical と一致するセッションだけを読み込む。
// ディレクトリが存在しない場合は空スライスと nil error を返す (未 harvest 状態を許容)。
func loadHarvestForProject(harvestDir, project string) ([]HarvestSession, error) {
	var out []HarvestSession
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
		recs, err := readHarvestFile(path, project)
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

// readHarvestFile は 1 つの harvest jsonl から project 一致 record を返す。
func readHarvestFile(path, project string) ([]HarvestSession, error) {
	f, err := os.Open(path) // #nosec G304 -- CLI: パスは WalkDir 由来
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var out []HarvestSession
	scanner := jsonlscan.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var rec HarvestSession
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

// extractInvokedSkills は harvest sessions から skill 名を dedup + sort した slice で返す。
// Mine の第一段 (対象 skill 候補セットの決定) で使う。
func extractInvokedSkills(recs []HarvestSession) []string {
	set := make(map[string]struct{})
	for _, r := range recs {
		for _, s := range r.SkillsInvoked {
			set[s] = struct{}{}
		}
	}
	out := make([]string, 0, len(set))
	for s := range set {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
