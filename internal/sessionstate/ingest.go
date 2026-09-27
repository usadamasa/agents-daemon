package sessionstate

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"time"
)

// statuslineInput は Claude Code が statusline コマンドの stdin に渡す JSON のうち､
// sidecar file に要る部分だけを受ける形｡値は pointer で受けて null と 0 を区別する｡
// 5 時間ウィンドウの切り替わりでは five_hour が {null, null} で届く (実機で観測) ため､
// この区別が無いと直前の resets_at を epoch 0 で上書きしてしまう｡
type statuslineInput struct {
	SessionID     string `json:"session_id"`
	ContextWindow struct {
		UsedPercentage *float64 `json:"used_percentage"`
	} `json:"context_window"`
	RateLimits *struct {
		FiveHour *statuslineWindow `json:"five_hour"`
		SevenDay *statuslineWindow `json:"seven_day"`
	} `json:"rate_limits"`
}

// statuslineWindow は stdin 側の 1 ウィンドウ｡resets_at を float64 で受けるのは､
// 実機では整数だが小数で届いても取りこぼさないため (int64 だと Unmarshal が失敗して
// rate-limits が書かれず､上限待ちの pane の起床時刻が消える)｡
type statuslineWindow struct {
	UsedPercentage *float64 `json:"used_percentage"`
	ResetsAt       *float64 `json:"resets_at"`
}

// IngestStatusline は statusline の stdin (input) から context/ と rate-limits/ の
// sidecar file を書く｡`agents-daemon ingest-statusline` の本体で､statusline の描画
// ごとに呼ばれる｡
//
//   - session_id が空なら何も書かない (判断材料が無い)
//   - context/<session_id>.json は毎回書く｡daemon は observed_at の新しさで
//     セッションの生死も見るため､書かない描画があると判定が古くなる
//   - rate-limits/<session_id>.json は rate_limits.five_hour.resets_at が非 null の
//     ときだけ書く｡無い (初回 API レスポンス前､rate_limits が届かない契約､ウィンドウの
//     切り替わり) ときは既存ファイルを残す
//
// 書き込みは同一ディレクトリの一時ファイル経由の rename で原子的に行う｡
func (s Store) IngestStatusline(input []byte, now time.Time) error {
	var in statuslineInput
	if err := json.Unmarshal(input, &in); err != nil {
		return fmt.Errorf("statusline の入力のパースに失敗: %w", err)
	}
	contextPath, err := sessionPath(s.Context, in.SessionID, ".json")
	if err != nil || contextPath == "" {
		return err
	}
	rateLimitPath, err := sessionPath(s.RateLimits, in.SessionID, ".json")
	if err != nil {
		return err
	}

	observedAt := now.Unix()
	ctx := contextFile{
		SessionID:      in.SessionID,
		UsedPercentage: roundPercent(in.ContextWindow.UsedPercentage),
		ObservedAt:     observedAt,
	}
	if err := writeJSONAtomic(contextPath, ".context.*", ctx); err != nil {
		return err
	}

	rl, ok := in.rateLimitFile(observedAt)
	if !ok {
		return nil
	}
	return writeJSONAtomic(rateLimitPath, ".rate-limits.*", rl)
}

// rateLimitFile は stdin の rate_limits を書き出す形へ写す｡five_hour.resets_at が
// 無ければ (false) で､呼び出し元は書かない｡seven_day も resets_at があるときだけ付ける｡
func (in *statuslineInput) rateLimitFile(observedAt int64) (rateLimitFile, bool) {
	if in.RateLimits == nil || in.RateLimits.FiveHour == nil || in.RateLimits.FiveHour.ResetsAt == nil {
		return rateLimitFile{}, false
	}
	out := rateLimitFile{
		FiveHour:   in.RateLimits.FiveHour.windowFile(),
		ObservedAt: observedAt,
		SessionID:  in.SessionID,
	}
	if sd := in.RateLimits.SevenDay; sd != nil && sd.ResetsAt != nil {
		w := sd.windowFile()
		out.SevenDay = &w
	}
	return out, true
}

// windowFile は stdin 側のウィンドウを書き出す形へ写す｡呼び出し元が ResetsAt の非 nil を
// 保証する｡used_percentage だけが null の描画は実機で見ていないが､読み手 (readRateLimit)
// は null も 0 に読むので 0 で書く｡
func (w *statuslineWindow) windowFile() windowFile {
	return windowFile{
		UsedPercentage: roundPercent(w.UsedPercentage),
		ResetsAt:       int64(*w.ResetsAt),
	}
}

// roundPercent は使用率を整数へ丸める (nil は 0)｡jq の round と同じ四捨五入
// (0.5 は 0 から遠い側へ) にして､旧 statusline の書き出しと同じ値になるようにする｡
func roundPercent(v *float64) float64 {
	if v == nil {
		return 0
	}
	return math.Round(*v)
}

// writeJSONAtomic は v を JSON 1 行 (末尾に改行) にして path へ原子的に書く｡
func writeJSONAtomic(path, tmpPattern string, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("%s の JSON 化に失敗: %w", filepath.Base(path), err)
	}
	return writeFileAtomic(path, tmpPattern, append(data, '\n'))
}

// writeFileAtomic は data を path へ原子的に書く｡
// 一時ファイルは path と同じディレクトリに tmpPattern の名前で作る｡別のファイル
// システムだと rename が原子的でなくなり､daemon が書きかけを読むため｡rename 前に
// 失敗したら一時ファイルを消す (消し損ねても housekeeping が mtime で拾う)｡
func writeFileAtomic(path, tmpPattern string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("%s の作成に失敗: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, tmpPattern)
	if err != nil {
		return fmt.Errorf("一時ファイルの作成に失敗: %w", err)
	}
	if err := writeAndClose(tmp, data); err != nil {
		return errors.Join(err, os.Remove(tmp.Name()))
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return errors.Join(fmt.Errorf("%s への置き換えに失敗: %w", path, err), os.Remove(tmp.Name()))
	}
	return nil
}

// writeAndClose は f へ data を書き切って閉じる｡Close の失敗も書き込みの失敗として返す｡
func writeAndClose(f *os.File, data []byte) error {
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return fmt.Errorf("一時ファイルへの書き込みに失敗: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("一時ファイルのクローズに失敗: %w", err)
	}
	return nil
}
