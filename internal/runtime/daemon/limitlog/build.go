package limitlog

import (
	"time"

	"github.com/usadamasa/agents-daemon/internal/multiplexer"
	"github.com/usadamasa/agents-daemon/internal/ratelimit"
	"github.com/usadamasa/agents-daemon/internal/runtime/config"
	"github.com/usadamasa/agents-daemon/internal/runtime/monitor"
	"github.com/usadamasa/agents-daemon/internal/turnfailure"
)

// Input は Build が出来事に写す、tick 1 回ぶんの材料。
type Input struct {
	At      time.Time
	Pane    multiplexer.Pane
	Outcome monitor.Outcome
	// State は Tick の後の状態機械。
	State monitor.PaneState
	// Evidence は Tick が判定に使った根拠 (monitor.Deps.Observe が渡す)。
	Evidence monitor.Evidence
	// TurnFailure はそのセッションの StopFailure の記録。無ければ nil。
	TurnFailure *turnfailure.Record
	// RateLimit は own、Account はアカウント全体の statusline の state。無ければ nil。
	RateLimit *ratelimit.State
	Account   *ratelimit.State
	Config    config.Config
}

// Build は tick の結果から出来事を組む。端末の大きさ (Geometry) は呼び手が後から足す。
func Build(in Input) Event {
	ev := Event{
		At:          in.At,
		PaneID:      in.Pane.PaneID,
		SessionID:   in.Pane.SessionID,
		CWD:         in.Pane.CWD,
		Outcome:     in.Outcome.String(),
		Kind:        in.Evidence.Kind.String(),
		Attempts:    in.State.Attempts,
		WaitUntil:   zeroableTime(in.State.WaitUntil),
		Record:      Record{AgentStatus: string(in.Pane.AgentStatus), Kind: in.Pane.AgentKind, Msg: in.Pane.AgentMessage},
		Transcript:  transcriptOf(in.Evidence.Turn),
		Reason:      in.Evidence.Turn.Reason,
		RateLimit:   windowOf(in.RateLimit),
		Account:     windowOf(in.Account),
		TurnFailure: turnFailureOf(in.TurnFailure),
		Settings:    Settings{ReadLines: in.Config.ReadLines, DetectionTailLines: in.Config.DetectionTailLines},
	}
	if in.Evidence.ScreenRead {
		ev.Screen, ev.Hint = in.Evidence.Screen, in.Evidence.Hint.String()
	}
	return ev
}

// transcriptOf は読んだ transcript の末尾を記録の形に写す。読めていなければ nil (欄ごと出さない)。
func transcriptOf(turn monitor.Turn) *Transcript {
	e := turn.Entry
	if e == nil {
		return nil
	}
	out := &Transcript{
		Path: turn.Path, UUID: e.UUID, Type: e.Type, Timestamp: e.Timestamp, Version: e.Version,
		Error: e.Error, APIErrorStatus: e.APIErrorStatus, Text: headRunes(e.Text, TextLimit),
	}
	if q := e.Quota; q != nil {
		out.Quota = &Quota{Status: q.Status, RateLimitType: q.RateLimitType, OverageStatus: q.OverageStatus, ResetsAt: zeroableTime(q.ResetsAt)}
	}
	return out
}

// windowOf は利用上限 state を記録の形に写す。無ければ nil (欄ごと出さない)。
func windowOf(state *ratelimit.State) *Window {
	if state == nil {
		return nil
	}
	return &Window{
		UsedPercentage: state.FiveHourUsed,
		ResetsAt:       state.FiveHourResetsAt,
		ObservedAt:     state.ObservedAt,
		SessionID:      state.SessionID,
	}
}

// turnFailureOf は turn の失敗の記録を出来事の形に写す。無ければ nil (欄ごと出さない)。
func turnFailureOf(rec *turnfailure.Record) *TurnFailure {
	if rec == nil {
		return nil
	}
	return &TurnFailure{
		Error:        rec.Error,
		ErrorDetails: rec.ErrorDetails,
		Message:      rec.Message,
		ObservedAt:   rec.ObservedAt,
	}
}

// zeroableTime はゼロ値の time.Time を nil にする (JSON で欄ごと省く)。
func zeroableTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

// headRunes は s を先頭 n rune までに切る。
func headRunes(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n])
}
