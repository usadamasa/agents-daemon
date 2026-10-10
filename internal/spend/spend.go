// Package spend は statusline が記録するセッションごとの cost の台帳｡
//
// 利用上限が API から届かない契約 (Enterprise の seat) では rate_limits が statusline の stdin に
// 来ないため､cost をセッション横断で合算して自分で出す｡出すのはその日の消費と､当月の消費
// (シートの月額上限に対する割合)｡
//
// 置き場は `${XDG_STATE_HOME:-~/.local/state}/agents-daemon/cost/<YYYY-MM-DD>/<session_id>.json`｡
// 日付で入れ子になり､保持期間を日付の名前で判定するので､session_id 単位の flat な sidecar を
// mtime で消す sessionstate とは形が違う｡daemon はこの台帳を読まない｡書くのも消すのも statusline｡
//
// 1 セッション 1 ファイルなので pane 間で競合しない｡cost.total_cost_usd はセッションの累計
// なので､日付を跨いだセッション (夜通しの pane､--continue) では前日までに計上した分
// (carried_usd) を引かないとその日の消費を過大に出す｡繰り越しはその日の最初の書き込みで
// 同じ session_id の過去の記録を合算して決め､合算側は total - carried を足す｡
//
// 日付は now のローカル時刻の日付｡UTC で切ると日本時間の午前 9 時前が前日に入る｡
// 月の区切りは実際のリセット (毎月 1 日 00:00 UTC) に対して 9 時間ずれるが､この合算は claude.ai と
// connectors の消費を含まない過小評価なので､厳密に合わせても精度は上がらない｡
package spend

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/usadamasa/agents-daemon/internal/statefile"
)

const (
	dayLayout = "2006-01-02"
	// retention は記録を残す期間｡当月の集計と繰り越しに過去の記録が要るため､1 か月より長く取る｡
	retention = 45 * 24 * time.Hour
)

// Ledger は台帳のディレクトリ (cost/)｡ゼロ値 (Dir が空) は何も書かず､合算はゼロを返す｡
type Ledger struct {
	Dir string
}

// New は XDG state ディレクトリ (apppath.Paths.StateDir) から台帳の置き場を導出する｡
func New(stateDir string) Ledger {
	return Ledger{Dir: filepath.Join(stateDir, "cost")}
}

// record は <YYYY-MM-DD>/<session_id>.json の生の形｡
type record struct {
	SessionID  string  `json:"session_id"`
	TotalUSD   float64 `json:"total_cost_usd"`
	CarriedUSD float64 `json:"carried_usd"`
}

// spent はその日に計上する額 (累計から前日までの繰り越しを引いたもの)｡
func (r record) spent() float64 {
	return r.TotalUSD - r.CarriedUSD
}

// Spend は合算結果 (USD)｡
type Spend struct {
	// Day は now と同じ日の全セッションの消費｡
	Day float64
	// Month は now と同じ月の全セッションの消費｡
	Month float64
}

// Record は sessionID の累計 cost (totalUSD) を now の日付の記録へ書く｡statusline の描画ごとに呼ばれる｡
//
//   - sessionID が空なら何も書かない
//   - その日の最初の書き込みでは､保持期間を過ぎた日を先に消し (後にすると消す予定の記録まで
//     繰り越しに混ざる)､過去の記録から繰り越しを決める
//   - 2 回目以降は累計が前回と同じなら書き直さない (毎描画で書かないため)｡変わっていれば
//     繰り越しを保ったまま書き直す
func (l Ledger) Record(sessionID string, totalUSD float64, now time.Time) error {
	if l.Dir == "" || sessionID == "" {
		return nil
	}
	if !statefile.ValidSessionID(sessionID) {
		return fmt.Errorf("session ID %q はファイル名に使えない文字を含む", sessionID)
	}
	day := now.Format(dayLayout)
	path := filepath.Join(l.Dir, day, sessionID+".json")

	rec := record{SessionID: sessionID, TotalUSD: totalUSD}
	prev, ok := readRecord(path)
	switch {
	case ok && prev.TotalUSD == totalUSD:
		return nil
	case ok:
		rec.CarriedUSD = prev.CarriedUSD
	default:
		if err := l.prune(now); err != nil {
			return err
		}
		carried, err := l.carriedBefore(sessionID, day)
		if err != nil {
			return err
		}
		rec.CarriedUSD = carried
	}
	return statefile.WriteJSONAtomic(path, ".cost.*", rec)
}

// carriedBefore は day 以外の日の記録から､sessionID が既に計上した額の合計を返す｡
// 過去の記録は繰り越しを引いた後の値なので､spent を足すと「これまでに計上した合計」になる｡
func (l Ledger) carriedBefore(sessionID, day string) (float64, error) {
	days, err := l.days()
	if err != nil {
		return 0, err
	}
	var sum float64
	for _, d := range days {
		if d == day {
			continue
		}
		if r, ok := readRecord(filepath.Join(l.Dir, d, sessionID+".json")); ok {
			sum += r.spent()
		}
	}
	return sum, nil
}

// Sum は now と同じ日と同じ月の全セッションの消費を合算する｡
// 壊れたファイルと日付でない名前のエントリは読み飛ばす｡記録が無ければゼロ値｡
func (l Ledger) Sum(now time.Time) (Spend, error) {
	var spend Spend
	days, err := l.days()
	if err != nil {
		return spend, err
	}
	day := now.Format(dayLayout)
	month := now.Format("2006-01") + "-"
	for _, d := range days {
		if !strings.HasPrefix(d, month) {
			continue
		}
		v, err := sumDay(filepath.Join(l.Dir, d))
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

// sumDay は 1 日分のディレクトリにある全セッションの spent を足す｡
func sumDay(dir string) (float64, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, fmt.Errorf("%s の走査に失敗: %w", dir, err)
	}
	var sum float64
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		if r, ok := readRecord(filepath.Join(dir, entry.Name())); ok {
			sum += r.spent()
		}
	}
	return sum, nil
}

// days は台帳直下の日付名のディレクトリを名前順で返す｡ディレクトリが無い､またはゼロ値なら空｡
func (l Ledger) days() ([]string, error) {
	if l.Dir == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(l.Dir)
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
		if _, err := time.Parse(dayLayout, entry.Name()); err != nil {
			continue
		}
		days = append(days, entry.Name())
	}
	return days, nil
}

// prune は保持期間を過ぎた日のディレクトリを丸ごと消す｡
// 日付はディレクトリ名で比べる (mtime は当日の書き込みで更新されるので使えない)｡
func (l Ledger) prune(now time.Time) error {
	days, err := l.days()
	if err != nil {
		return err
	}
	cutoff := now.Add(-retention).Format(dayLayout)
	for _, d := range days {
		if d >= cutoff {
			continue
		}
		if err := os.RemoveAll(filepath.Join(l.Dir, d)); err != nil {
			return fmt.Errorf("%s の削除に失敗: %w", d, err)
		}
	}
	return nil
}

// readRecord は記録を読む｡無い・壊れているなら false｡
func readRecord(path string) (record, bool) {
	data, err := os.ReadFile(path) // #nosec G304 -- 呼び出し元が session ID を検証済みのパスを渡す
	if err != nil {
		return record{}, false
	}
	var r record
	if err := json.Unmarshal(data, &r); err != nil {
		return record{}, false
	}
	return r, true
}
