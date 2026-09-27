package monitor

import (
	"context"
	"fmt"
	"time"

	"github.com/usadamasa/agents-daemon/internal/detect"
	"github.com/usadamasa/agents-daemon/internal/herdrcli"
	"github.com/usadamasa/agents-daemon/internal/sessionstate"
)

// compact 周りの自動化は 3 段に分かれ､いずれも画面の文字列を判定に使わない｡
// 使うのは別プロセスが書いたファイルの有無と mtime だけ｡
//
//	1 段目  context 使用率が閾値超え          → /compact-prep を投入
//	2 段目  compact-prep の state file が出た → /compact を投入
//	3 段目  圧縮完了 marker が置かれた        → 作業の再開を促す
//
// 3 段目は 1・2 段目を経由しない compact (ユーザーが手で打った / Claude Code 本体の
// autocompact) でも動く｡marker を書くのは PostCompact hook であってこのツールでは
// ないため｡
//
// 2 段目と 3 段目の「もう送った」の判定は､いずれも対象ファイルの mtime が
// 自分の送信時刻より新しいかで見る｡存在だけを条件にすると､消し忘れた 1 つの
// ファイルで同じ pane を延々と突くことになる｡
//
// PaneState は in-memory なので､送信の合間に daemon が再起動すると送信時刻が
// 失われ､1 段目からやり直しになる｡害は compact-prep が 1 回余計に走るだけ｡

// tickCompact は compact 周りの 3 段と､画面判定による fallback を順に評価する｡
// 各段は送るものが無ければ OutcomeMonitoring を返し､次の段へ流れる｡
//
// 画面判定 (tickCompactStall) を最後に置くのは､marker 経路が届かなかったときの
// 二段目の受け皿として残しているため｡PostCompact hook が起動しなかった pane
// (hook の timeout､stdin の session_id が空) では marker が置かれず 3 段目が動かない｡
func tickCompact(
	ctx context.Context, deps Deps, pane herdrcli.Pane, ps *PaneState,
	screen string, now time.Time,
) (Outcome, error) {
	// loader が差されていない (テスト､あるいは compact の自動化を持たない
	// 呼び出し元) なら state は無し扱いで､以降の段は何もしない｡
	var cs *sessionstate.Compact
	if deps.CompactState != nil {
		cs = deps.CompactState(pane.SessionID())
	}

	if outcome, err := tickCompactAuto(ctx, deps, pane, ps, cs, screen, now); outcome != OutcomeMonitoring || err != nil {
		return outcome, err
	}
	if outcome, err := tickCompactResume(ctx, deps, pane, ps, cs, screen, now); outcome != OutcomeMonitoring || err != nil {
		return outcome, err
	}
	return tickCompactStall(ctx, deps, pane, ps, screen, now)
}

// tickCompactAuto は 1 段目と 2 段目を処理する｡
// 送るものが無ければ OutcomeMonitoring を返し､呼び出し元が次の段へ流す｡
func tickCompactAuto(
	ctx context.Context, deps Deps, pane herdrcli.Pane, ps *PaneState,
	cs *sessionstate.Compact, screen string, now time.Time,
) (Outcome, error) {
	if !deps.Config.CompactAutoEnabled {
		// 途中で機能を切られたときに pending が残らないよう畳む｡cs が nil
		// (session ID が無い / 読み込みが一度失敗した) だけでは畳まない｡
		ps.CompactPrepSentAt = time.Time{}
		return OutcomeMonitoring, nil
	}
	if cs == nil {
		return OutcomeMonitoring, nil
	}

	if !ps.CompactPrepSentAt.IsZero() {
		return tickCompactAutoPending(ctx, deps, pane, ps, cs, screen, now)
	}

	cooldown := time.Duration(deps.Config.CompactAutoCooldownMinutes) * time.Minute
	if now.Sub(ps.CompactAutoSentAt) < cooldown {
		return OutcomeMonitoring, nil
	}
	// 使用率は statusline が毎描画で書き直す｡古い観測しか無い pane は
	// そのセッションが生きていないか描画が止まっているので触らない｡
	maxAge := time.Duration(deps.Config.StateMaxAgeSeconds) * time.Second
	if !cs.ContextFresh(now, maxAge) || cs.UsedPercentage < deps.Config.CompactAutoThresholdPercent {
		return OutcomeMonitoring, nil
	}
	if !canSendTo(deps, pane, screen) {
		return OutcomeMonitoring, nil
	}

	ps.CompactPrepSentAt = now
	ps.CompactAutoSentAt = now
	if err := sendPrompt(ctx, deps, pane, ps, deps.Config.CompactAutoPrepMessage, now); err != nil {
		return OutcomeSendError, fmt.Errorf("pane %s への compact-prep 投入に失敗: %w", pane.PaneID, err)
	}
	return OutcomeCompactPrepSent, nil
}

// tickCompactAutoPending は compact-prep の完了を待っている状態を処理する｡
func tickCompactAutoPending(
	ctx context.Context, deps Deps, pane herdrcli.Pane, ps *PaneState,
	cs *sessionstate.Compact, screen string, now time.Time,
) (Outcome, error) {
	if cs.HasPrep && cs.PrepWrittenAt.After(ps.CompactPrepSentAt) {
		if !canSendTo(deps, pane, screen) {
			return OutcomeMonitoring, nil
		}
		ps.CompactPrepSentAt = time.Time{}
		ps.CompactAutoSentAt = now
		if err := sendPrompt(ctx, deps, pane, ps, deps.Config.CompactAutoMessage, now); err != nil {
			return OutcomeSendError, fmt.Errorf("pane %s への compact 投入に失敗: %w", pane.PaneID, err)
		}
		return OutcomeCompactSent, nil
	}

	timeout := time.Duration(deps.Config.CompactAutoPrepTimeoutMinutes) * time.Minute
	if now.Sub(ps.CompactPrepSentAt) >= timeout {
		// state file が出ないまま時間切れ｡CompactAutoSentAt を打ち直して､
		// 次の投入までクールダウンぶん待たせる (即座に再投入して往復しないため)｡
		ps.CompactPrepSentAt = time.Time{}
		ps.CompactAutoSentAt = now
		return OutcomeCompactPrepTimeout, nil
	}
	return OutcomeMonitoring, nil
}

// tickCompactResume は 3 段目を処理する｡圧縮完了 marker を見て作業の再開を促す｡
//
// marker を消すのは UserPromptSubmit 側の復旧 hook で､そこで state file の内容が
// 会話へ注入される｡だでユーザーが自分で次のプロンプトを打った場合､marker は
// 消えていてこの段は何もしない｡
func tickCompactResume(
	ctx context.Context, deps Deps, pane herdrcli.Pane, ps *PaneState,
	cs *sessionstate.Compact, screen string, now time.Time,
) (Outcome, error) {
	if !deps.Config.CompactResumeEnabled || cs == nil || !cs.HasCompacted {
		return OutcomeMonitoring, nil
	}
	// 自分が送った再開より後に圧縮されていなければ､同じ marker を見ている｡
	if !cs.CompactedAt.After(ps.CompactResumedAt) {
		return OutcomeMonitoring, nil
	}
	// 圧縮直後は人が自分で続きを打つことがある｡その間を空ける｡
	delay := time.Duration(deps.Config.CompactResumeDelaySeconds) * time.Second
	if now.Sub(cs.CompactedAt) < delay {
		return OutcomeMonitoring, nil
	}
	if !canSendTo(deps, pane, screen) {
		return OutcomeMonitoring, nil
	}

	ps.CompactResumedAt = now
	// 画面判定の fallback にも送信済みとして伝える｡伝えないと､境界行が拾える
	// 版の Claude Code では marker 経路の直後にもう一度突くことになる｡
	ps.CompactNudgedAt = now
	if err := sendPrompt(ctx, deps, pane, ps, deps.Config.CompactStallMessage, now); err != nil {
		return OutcomeSendError, fmt.Errorf("pane %s への再開送信に失敗: %w", pane.PaneID, err)
	}
	return OutcomeCompactResumed, nil
}

// canSendTo は pane へ今キー入力を送ってよいかを返す｡
//
// 2 つを要求する｡pane が働いていないこと (working な pane へ送るとキューに入り､
// 進行中の作業の直後に実行される) と､入力欄が空であること (残った文字に送信文字列が
// 連結される)｡どちらも実機で確認した挙動｡
func canSendTo(deps Deps, pane herdrcli.Pane, screen string) bool {
	return isIdlePane(pane) && detect.PromptIsEmpty(screen, deps.Config.DetectionTailLines)
}
