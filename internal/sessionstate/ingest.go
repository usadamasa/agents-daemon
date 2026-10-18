package sessionstate

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"time"

	"github.com/usadamasa/agents-daemon/internal/ratelimit"
	"github.com/usadamasa/agents-daemon/internal/sessioncontext"
	"github.com/usadamasa/agents-daemon/internal/statefile"
)

// statuslineInput は Claude Code が statusline コマンドの stdin に渡す JSON のうち、
// sidecar file に要る部分だけを受ける形。値は pointer で受けて null と 0 を区別する。
// 5 時間ウィンドウの切り替わりでは five_hour が {null, null} で届く (実機で観測) ため、
// この区別が無いと直前の resets_at を epoch 0 で上書きしてしまう。
type statuslineInput struct {
	SessionID string `json:"session_id"`
	// SessionName は --name か /rename で付けた名前、無ければ Claude Code が付けた題。どちらも無ければ来ない。
	// cc-premise: 2.1.302 statusline session_name は custom title、無ければ AI の題 (ListAgents の名前とは別。衝突で -dc のような接尾辞が付いた名前は来ない)
	SessionName   string `json:"session_name"`
	ContextWindow struct {
		UsedPercentage *float64 `json:"used_percentage"`
	} `json:"context_window"`
	RateLimits *struct {
		FiveHour *statuslineWindow `json:"five_hour"`
		SevenDay *statuslineWindow `json:"seven_day"`
	} `json:"rate_limits"`
}

// statuslineWindow は stdin 側の 1 ウィンドウ。resets_at を float64 で受けるのは、
// 実機では整数だが小数で届いても取りこぼさないため (int64 だと Unmarshal が失敗して
// rate-limits が書かれず、上限待ちの pane の起床時刻が消える)。
type statuslineWindow struct {
	UsedPercentage *float64 `json:"used_percentage"`
	ResetsAt       *float64 `json:"resets_at"`
}

// IngestStatusline は statusline の stdin (input) から context/ と rate-limits/ の
// sidecar file を書く。`agents-daemon ingest-statusline` の本体で、statusline の描画
// ごとに呼ばれる。
//
//   - session_id が空なら何も書かない (判断材料が無い)
//   - context/<session_id>.json は毎回書く。block と session の対応は最新の記録を採るので、
//     書かない描画があると対応が古くなる
//   - rate-limits/<session_id>.json は rate_limits.five_hour.resets_at が非 null の
//     ときだけ書く。無い (初回 API レスポンス前、rate_limits が届かない契約、ウィンドウの
//     切り替わり) ときは既存ファイルを残す
//
// 端末多重化ツールの場所はプロセスの環境変数から読む (IngestStatuslineWithEnv)。
func (s Store) IngestStatusline(input []byte, now time.Time) error {
	return s.IngestStatuslineWithEnv(input, os.Getenv, now)
}

// IngestStatuslineWithEnv は IngestStatusline の本体で、環境変数を getenv から読む。
// statusline の command は Claude Code の子プロセスなので REX_BLOCK を
// 継承しており、その場所を context/<session_id>.json の multiplexer に書く
// (sessioncontext.MultiplexerRefFromEnv)。
func (s Store) IngestStatuslineWithEnv(input []byte, getenv func(string) string, now time.Time) error {
	var in statuslineInput
	if err := json.Unmarshal(input, &in); err != nil {
		return fmt.Errorf("statusline の入力のパースに失敗: %w", err)
	}
	if in.SessionID == "" {
		return nil
	}
	// 片方だけ書いて止まらないよう、どちらかを書く前に検証する
	if !statefile.ValidSessionID(in.SessionID) {
		return fmt.Errorf("session ID %q はファイル名に使えない文字を含む", in.SessionID)
	}

	rec := sessioncontext.Record{
		SessionID:      in.SessionID,
		SessionName:    in.SessionName,
		UsedPercentage: roundPercent(in.ContextWindow.UsedPercentage),
		ObservedAt:     now,
	}
	if ref, ok := sessioncontext.MultiplexerRefFromEnv(getenv); ok {
		rec.Multiplexer = &ref
	}
	if err := s.Context.Write(rec); err != nil {
		return err
	}

	fiveHour, sevenDay, ok := in.rateLimitWindows()
	if !ok {
		return nil
	}
	return s.RateLimits.Write(in.SessionID, now, fiveHour, sevenDay)
}

// rateLimitWindows は stdin の rate_limits を書き出すウィンドウへ写す。five_hour.resets_at が
// 無ければ (false) で、呼び出し元は書かない。seven_day も resets_at があるときだけ付ける。
func (in *statuslineInput) rateLimitWindows() (fiveHour ratelimit.Window, sevenDay *ratelimit.Window, ok bool) {
	if in.RateLimits == nil || in.RateLimits.FiveHour == nil || in.RateLimits.FiveHour.ResetsAt == nil {
		return ratelimit.Window{}, nil, false
	}
	fiveHour = in.RateLimits.FiveHour.window()
	if sd := in.RateLimits.SevenDay; sd != nil && sd.ResetsAt != nil {
		w := sd.window()
		sevenDay = &w
	}
	return fiveHour, sevenDay, true
}

// window は stdin 側のウィンドウを書き出す形へ写す。呼び出し元が ResetsAt の非 nil を
// 保証する。used_percentage だけが null の描画は実機で見ていないが、読み手 (ratelimit.Store.Load)
// は null も 0 に読むので 0 で書く。
func (w *statuslineWindow) window() ratelimit.Window {
	return ratelimit.Window{
		UsedPercentage: roundPercent(w.UsedPercentage),
		ResetsAt:       time.Unix(int64(*w.ResetsAt), 0),
	}
}

// roundPercent は使用率を整数へ丸める (nil は 0)。jq の round と同じ四捨五入
// (0.5 は 0 から遠い側へ) にして、旧 statusline の書き出しと同じ値になるようにする。
func roundPercent(v *float64) float64 {
	if v == nil {
		return 0
	}
	return math.Round(*v)
}
