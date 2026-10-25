package record

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/usadamasa/agents-daemon/internal/dream/jsonlscan"
	"github.com/usadamasa/agents-daemon/internal/statefile"
)

// Stats は書き込み結果サマリ。
type Stats struct {
	Written          int
	SkippedDuplicate int
}

// Write は records を YYYY-MM-DD.jsonl 単位で outDir/harvest/ に書く。
// 既存ファイルの内容と session_id ベースで dedup (last-wins) してから rewrite する。
//
// idempotent: 同じ input で複数回叩いても最終ファイル内容は変わらない。
func Write(outDir string, records []Record) (Stats, error) {
	harvestDir := filepath.Join(outDir, "harvest")

	// 日付ごとに records を bucket 化する
	buckets := make(map[string][]Record)
	for _, r := range records {
		key := recordDateKey(r)
		buckets[key] = append(buckets[key], r)
	}

	stats := Stats{}
	for date, recs := range buckets {
		path := filepath.Join(harvestDir, date+".jsonl")
		existing, err := readExistingRecords(path)
		if err != nil {
			return stats, fmt.Errorf("既存 %s の読み込み失敗: %w", path, err)
		}

		merged := mergeAndDedup(existing, recs)
		stats.Written += len(recs)
		stats.SkippedDuplicate += len(existing) + len(recs) - len(merged)

		if err := writeJSONL(path, merged); err != nil {
			return stats, fmt.Errorf("書き込み失敗 %s: %w", path, err)
		}
	}
	return stats, nil
}

// recordDateKey は record を書き込む対象日 (JST の date 文字列)。
// ts_start がなければ ts_end、それも無ければ今日。
func recordDateKey(r Record) string {
	ts := r.TSStart
	if ts == "" {
		ts = r.TSEnd
	}
	if ts == "" {
		return time.Now().Format("2006-01-02")
	}
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return time.Now().Format("2006-01-02")
	}
	return t.Local().Format("2006-01-02")
}

// readExistingRecords は既存 jsonl から Record を復元する。
// ファイル未存在は空スライスを返す (エラーではない)。
func readExistingRecords(path string) ([]Record, error) {
	f, err := os.Open(path) // #nosec G304 -- CLI: 出力先ディレクトリ固定
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var out []Record
	sc := jsonlscan.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var r Record
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			continue // 破損行は skip (fail-open)
		}
		out = append(out, r)
	}
	return out, sc.Err()
}

// mergeAndDedup は session_id last-wins で 2 スライスをマージする。
// 結果は session_id で昇順ソートされる (deterministic output)。
func mergeAndDedup(existing, incoming []Record) []Record {
	byID := make(map[string]Record, len(existing)+len(incoming))
	for _, r := range existing {
		byID[r.SessionID] = r
	}
	for _, r := range incoming {
		byID[r.SessionID] = r // last-wins
	}
	out := make([]Record, 0, len(byID))
	for _, r := range byID {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].SessionID < out[j].SessionID
	})
	return out
}

// writeJSONL は records を 1 行 1 JSON で path へ原子的に書く (statefile)。
func writeJSONL(path string, records []Record) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	for _, r := range records {
		if err := enc.Encode(r); err != nil {
			return err
		}
	}
	return statefile.WriteFileAtomic(path, filepath.Base(path)+".*.tmp", buf.Bytes())
}
