package sessionstate

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// cost/<YYYY-MM-DD>/<session_id>.json は statusline がセッションの累計 cost を日付ごとに
// 記録したもの｡利用上限が API から届かない契約 (Enterprise の seat) では rate_limits が
// stdin に来ないため､cost をセッション横断で合算して自分で出す｡
//
// 1 セッション 1 ファイルなので pane 間で競合しない｡cost.total_cost_usd はセッションの累計
// なので､日付を跨いだセッション (夜通しの pane､--continue) では前日までに計上した分
// (carried_usd) を引かないとその日の消費を過大に出す｡繰り越しはその日の最初の書き込みで
// 同じ session_id の過去の記録を合算して決め､合算側は total - carried を足す｡
//
// 日付は now のローカル時刻の日付｡UTC で切ると日本時間の午前 9 時前が前日に入る｡
const (
	costDayLayout = "2006-01-02"
	// costRetention は cost の記録を残す期間｡当月の集計と繰り越しに過去の記録が要るため､
	// 1 か月より長く取る｡
	costRetention = 45 * 24 * time.Hour
)

// costFile は cost/<YYYY-MM-DD>/<session_id>.json の生の形｡
type costFile struct {
	SessionID  string  `json:"session_id"`
	TotalUSD   float64 `json:"total_cost_usd"`
	CarriedUSD float64 `json:"carried_usd"`
}

// spent はその日に計上する額 (累計から前日までの繰り越しを引いたもの)｡
func (c costFile) spent() float64 {
	return c.TotalUSD - c.CarriedUSD
}

// Spend は cost の合算結果 (USD)｡
type Spend struct {
	// Day は now と同じ日の全セッションの消費｡
	Day float64
	// Month は now と同じ月の全セッションの消費｡
	Month float64
}

// RecordCost は sessionID の累計 cost (totalUSD) を now の日付の記録へ書く｡
// statusline の描画ごとに呼ばれる｡
//
//   - sessionID が空なら何も書かない
//   - その日の最初の書き込みでは､保持期間を過ぎた日を先に消し (後にすると消す予定の記録まで
//     繰り越しに混ざる)､過去の記録から繰り越しを決める
//   - 2 回目以降は累計が前回と同じなら書き直さない (毎描画で書かないため)｡変わっていれば
//     繰り越しを保ったまま書き直す
func (s Store) RecordCost(sessionID string, totalUSD float64, now time.Time) error {
	if s.Cost == "" || sessionID == "" {
		return nil
	}
	if !sessionIDPattern.MatchString(sessionID) {
		return fmt.Errorf("session ID %q はファイル名に使えない文字を含む", sessionID)
	}
	day := now.Format(costDayLayout)
	path := filepath.Join(s.Cost, day, sessionID+".json")

	record := costFile{SessionID: sessionID, TotalUSD: totalUSD}
	prev, ok := readCostFile(path)
	switch {
	case ok && prev.TotalUSD == totalUSD:
		return nil
	case ok:
		record.CarriedUSD = prev.CarriedUSD
	default:
		if _, err := s.pruneCostDays(now); err != nil {
			return err
		}
		carried, err := s.carriedBefore(sessionID, day)
		if err != nil {
			return err
		}
		record.CarriedUSD = carried
	}
	return writeJSONAtomic(path, ".cost.*", record)
}

// carriedBefore は day 以外の日の記録から､sessionID が既に計上した額の合計を返す｡
// 過去の記録は繰り越しを引いた後の値なので､spent を足すと「これまでに計上した合計」になる｡
func (s Store) carriedBefore(sessionID, day string) (float64, error) {
	days, err := s.costDays()
	if err != nil {
		return 0, err
	}
	var sum float64
	for _, d := range days {
		if d == day {
			continue
		}
		if c, ok := readCostFile(filepath.Join(s.Cost, d, sessionID+".json")); ok {
			sum += c.spent()
		}
	}
	return sum, nil
}

// SumCost は now と同じ日と同じ月の全セッションの消費を合算する｡
// 壊れたファイルと日付でない名前のエントリは読み飛ばす｡記録が無ければゼロ値｡
func (s Store) SumCost(now time.Time) (Spend, error) {
	var spend Spend
	days, err := s.costDays()
	if err != nil {
		return spend, err
	}
	day := now.Format(costDayLayout)
	month := now.Format("2006-01") + "-"
	for _, d := range days {
		if !strings.HasPrefix(d, month) {
			continue
		}
		v, err := sumCostDay(filepath.Join(s.Cost, d))
		if err != nil {
			return spend, err
		}
		spend.Month += v
		if d == day {
			spend.Day += v
		}
	}
	return spend, nil
}

// sumCostDay は 1 日分のディレクトリにある全セッションの spent を足す｡
func sumCostDay(dir string) (float64, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, fmt.Errorf("%s の走査に失敗: %w", dir, err)
	}
	var sum float64
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		if c, ok := readCostFile(filepath.Join(dir, entry.Name())); ok {
			sum += c.spent()
		}
	}
	return sum, nil
}

// costDays は cost/ 直下の日付名のディレクトリを名前順で返す｡ディレクトリが無い､または
// ゼロ値の Store なら空｡
func (s Store) costDays() ([]string, error) {
	if s.Cost == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(s.Cost)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("cost ディレクトリの走査に失敗: %w", err)
	}
	var days []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if _, err := time.Parse(costDayLayout, entry.Name()); err != nil {
			continue
		}
		days = append(days, entry.Name())
	}
	return days, nil
}

// pruneCostDays は保持期間を過ぎた日のディレクトリを丸ごと消し､消した数を返す｡
// 日付はディレクトリ名で比べる (mtime は当日の書き込みで更新されるので使えない)｡
func (s Store) pruneCostDays(now time.Time) (int, error) {
	days, err := s.costDays()
	if err != nil {
		return 0, err
	}
	cutoff := now.Add(-costRetention).Format(costDayLayout)
	removed := 0
	for _, d := range days {
		if d >= cutoff {
			continue
		}
		if err := os.RemoveAll(filepath.Join(s.Cost, d)); err != nil {
			return removed, fmt.Errorf("%s の削除に失敗: %w", d, err)
		}
		removed++
	}
	return removed, nil
}

// readCostFile は cost file を読む｡無い・壊れているなら false｡
func readCostFile(path string) (costFile, bool) {
	data, err := os.ReadFile(path) // #nosec G304 -- 呼び出し元が session ID を検証済みのパスを渡す
	if err != nil {
		return costFile{}, false
	}
	var c costFile
	if err := json.Unmarshal(data, &c); err != nil {
		return costFile{}, false
	}
	return c, true
}
