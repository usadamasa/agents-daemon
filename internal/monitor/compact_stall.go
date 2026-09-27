package monitor

import (
	"context"
	"fmt"
	"time"

	"github.com/usadamasa/agents-daemon/internal/detect"
	"github.com/usadamasa/agents-daemon/internal/herdrcli"
)

// tickCompactStall は「上限とは無関係に､compact 直後に入力待ちで止まった pane」を
// 突く｡上限の検知とは別系統の判定なので､安全策も別に用意してある｡
//
// 上限検知の安全策は「二重シグナル + statusline の使用率ゲート」だが､compact 停止に
// 相当する裏取りは無い｡誤って発火すると､ユーザーが意図して置いた idle な pane へ
// プロンプトを打ち込むことになる｡だで次を全て満たしたときだけ送る:
//
//   - compactStallEnabled が true
//   - 画面が limit にも transient にも該当しない (呼び出し元が保証)
//   - agent_status が idle / done (working / blocked / unknown には触らない)
//   - compact 境界の後ろに Claude の応答が無く､空の入力行で止まっている
//   - その画面が compactStallQuietSeconds のあいだ変わっていない
//   - 直近の送信から compactStallCooldownMinutes 以上経っている
func tickCompactStall(ctx context.Context, deps Deps, pane herdrcli.Pane, ps *PaneState, screen string, now time.Time) (Outcome, error) {
	cfg := deps.Config

	signature, ok := detect.CompactStallSignature(screen, cfg.DetectionTailLines)
	if !cfg.CompactStallEnabled || !isIdlePane(pane) || !ok {
		// 停止が解けた (応答が流れた / 入力が始まった / 作業中)｡次に止まったら
		// 新しい停止として数え直す｡
		ps.CompactSignature = ""
		ps.CompactSeenAt = time.Time{}
		ps.CompactNudges = 0
		return OutcomeMonitoring, nil
	}

	if ps.CompactSignature != signature {
		// 初めて見た形｡ここから安定を測り始める｡compact 直後に Claude が
		// 考え始める間や､ユーザーが入力を始める間をこの猶予で跨ぐ｡
		ps.CompactSignature = signature
		ps.CompactSeenAt = now
		return OutcomeMonitoring, nil
	}
	if now.Sub(ps.CompactSeenAt) < time.Duration(cfg.CompactStallQuietSeconds)*time.Second {
		return OutcomeMonitoring, nil
	}
	if now.Sub(ps.CompactNudgedAt) < time.Duration(cfg.CompactStallCooldownMinutes)*time.Minute {
		// 送っても画面が変わらないことがある｡同じ pane を突き続けない｡
		return OutcomeMonitoring, nil
	}
	if ps.CompactNudges >= cfg.CompactStallMaxNudges {
		// cooldown を跨いで何度送っても進まない run は詰まっている｡突き続けるより
		// 止めて人が見られる状態にする｡署名は残し､停止が解けるまで送らない｡
		return OutcomeCompactNudgeCapped, nil
	}

	ps.CompactNudgedAt = now
	ps.CompactNudges++
	ps.CompactSignature = ""
	ps.CompactSeenAt = time.Time{}

	if err := sendPrompt(ctx, deps, pane, ps, deps.Config.CompactStallMessage, now); err != nil {
		return OutcomeSendError, fmt.Errorf("pane %s への継続要求の送信に失敗: %w", pane.PaneID, err)
	}
	return OutcomeCompactNudged, nil
}

// isIdlePane は pane が入力待ちかを返す｡
//
// 上限検知では agent_status を判定に使わない (上限で止まった pane が working と
// 報告され続けることがあるため) が､ここでは逆に「働いていないこと」を要求する側なので
// 使ってよい｡working を idle と誤報する事例は観測されておらず､誤っても
// 「送らない」側へ倒れる｡
func isIdlePane(pane herdrcli.Pane) bool {
	return pane.AgentStatus == herdrcli.AgentStatusIdle || pane.AgentStatus == herdrcli.AgentStatusDone
}

// sendPrompt は pane の入力欄へ text を打って Enter で投入する｡
// recoverPane と違って Escape は送らない｡閉じるべきメニューが無いうえ､万一
// ダイアログが開いていれば Escape がそれを勝手に answer してしまう｡ダイアログが
// 開いた pane は agent_status が idle にならないので､呼び出し元のゲートで既に
// 弾かれている｡
//
// Escape は入力行のクリアにも使えない (補完メニューを閉じるだけで文字は残る)｡
// だで呼び出し元は入力欄が空であることを確かめてから呼ぶこと｡
//
// 送る前に cache の ack を残す (cache_ack.go)｡
func sendPrompt(ctx context.Context, deps Deps, pane herdrcli.Pane, ps *PaneState, text string, now time.Time) error {
	ackBeforeSend(deps, pane, ps, text, now)
	if err := deps.Client.SendText(ctx, pane.PaneID, text); err != nil {
		return err
	}
	deps.Sleep(time.Duration(deps.Config.SubmitDelayMs) * time.Millisecond)
	return deps.Client.SendKeys(ctx, pane.PaneID, "enter")
}
