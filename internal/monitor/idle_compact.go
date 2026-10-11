package monitor

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/usadamasa/agents-daemon/internal/config"
	"github.com/usadamasa/agents-daemon/internal/multiplexer"
	"github.com/usadamasa/agents-daemon/internal/sessionstate"
)

// idle compact は､idle な pane の prompt cache が失効する前に 1 段目・2 段目を走らせる｡
// /compact の前に cache-idle-compacted/<session_id> を書き､それが active な間は 3 段目と
// 画面判定を送らない｡理由は skills/agents-daemon/references/compact.md の「idle compact」｡

// shortCacheTTL 以下の TTL では prep と compact の 2 段を失効前に終える余裕が無い｡
const shortCacheTTL = 5 * time.Minute

// IdleCompactGate は idle compact の 1 段目を送るか､送らないならどの条件で止まったかを表す｡
// Gate / WakeSource と同じ理由で判定は EvaluateIdleCompact の 1 箇所に置き､inspect もそれを呼ぶ
// (診断の説明と実際の挙動がずれないように)｡値の順は判定の順で､外側の条件から並ぶ｡
type IdleCompactGate int

const (
	// IdleCompactMarkerActive は前回の idle compact から利用者が戻っていないことを表す｡
	IdleCompactMarkerActive IdleCompactGate = iota
	// IdleCompactNoCache は cache の状態が無いことを表す (transcript_path が無い､cache write が
	// 無い､compact 後にまだ応答が無い)｡
	IdleCompactNoCache
	// IdleCompactExpired は cache が既に失効していることを表す｡
	IdleCompactExpired
	// IdleCompactBeforeWindow は失効前の窓 (失効の lead 秒前から) がまだ開いていないことを表す｡
	IdleCompactBeforeWindow
	// IdleCompactShortTTL は TTL が短く､失効前に 2 段を終えられないことを表す｡
	IdleCompactShortTTL
	// IdleCompactUsageUnknown は窓は開いているが､context_window_size か直近の応答のトークン数の
	// 記録が無く使用率を求められないことを表す (記録を始める前の版の statusline が書いた context)｡
	IdleCompactUsageUnknown
	// IdleCompactBelowThreshold は窓は開いているが､直近の応答の使用率が閾値未満であることを表す｡
	IdleCompactBelowThreshold
	// IdleCompactReady は送る条件を満たしていることを表す｡
	IdleCompactReady
)

// Held は「窓が開いているのに送らない」かを返す｡この 2 つだけを理由付きの Outcome で報告する｡
// 窓が開く前や失効後は毎 tick の常態で､報告しても読めるログにならない｡
func (g IdleCompactGate) Held() bool {
	return g == IdleCompactUsageUnknown || g == IdleCompactBelowThreshold
}

// String は IdleCompactGate を診断表示向けの日本語にする｡
func (g IdleCompactGate) String() string {
	switch g {
	case IdleCompactMarkerActive:
		return "前回の idle compact から利用者が戻っていない (marker が active)"
	case IdleCompactNoCache:
		return "cache の状態が無い (transcript_path が無い､cache write が無い､または compact 後にまだ応答が無い)"
	case IdleCompactExpired:
		return "cache は失効済み"
	case IdleCompactBeforeWindow:
		return "失効前の窓がまだ開いていない"
	case IdleCompactShortTTL:
		return "TTL が短く､失効前に prep と compact の 2 段を終えられない"
	case IdleCompactUsageUnknown:
		return "context_window_size か直近の応答のトークン数の記録が無く､使用率を求められない"
	case IdleCompactBelowThreshold:
		return "直近の応答の使用率が閾値未満"
	case IdleCompactReady:
		return "条件を満たしている (送る)"
	default:
		return "不明"
	}
}

// IdleCompactCheck は EvaluateIdleCompact の結果｡Gate に加えて､比較に使った使用率を持つ｡
type IdleCompactCheck struct {
	Gate IdleCompactGate
	// UsedPercentage は直近の応答から求めた使用率｡Gate が IdleCompactUsageUnknown より後まで
	// 進んだときだけ意味を持つ｡
	UsedPercentage float64
	// ThresholdPercent は比較した閾値 (cacheIdleCompactThresholdPercent)｡
	ThresholdPercent float64
}

// String は判定を理由の文にする｡閾値未満のときは比較した値を添える｡
func (c IdleCompactCheck) String() string {
	if c.Gate == IdleCompactBelowThreshold {
		return fmt.Sprintf("%s (%.0f%% < %.0f%%)", c.Gate, c.UsedPercentage, c.ThresholdPercent)
	}
	return c.Gate.String()
}

// EvaluateIdleCompact は idle compact の 1 段目の判定｡pane の状態 (idle､入力欄が空､cooldown) は
// 呼び出し元が見る｡ここで見るのは cache と context の state だけ｡
//
// TTL の短さを使用率より先に見るのは､5m TTL では窓が常に開いているため｡使用率を先に見ると
// 5m TTL の pane はターンのたびに Held を報告する｡
func EvaluateIdleCompact(cfg config.Config, cs *sessionstate.Compact, cache *sessionstate.Cache, now time.Time) IdleCompactCheck {
	check := IdleCompactCheck{ThresholdPercent: cfg.CacheIdleCompactThresholdPercent}
	lead := time.Duration(cfg.CacheIdleCompactLeadSeconds) * time.Second
	switch {
	case cs.IdleCompactActive(cache):
		check.Gate = IdleCompactMarkerActive
	case cache == nil:
		check.Gate = IdleCompactNoCache
	case cache.Expired(now):
		check.Gate = IdleCompactExpired
	case now.Before(cache.ExpiresAt().Add(-lead)):
		check.Gate = IdleCompactBeforeWindow
	case cache.TTL <= shortCacheTTL || cache.TTL <= lead:
		check.Gate = IdleCompactShortTTL
	default:
		pct, ok := cs.CacheUsedPercentage(cache)
		check.UsedPercentage = pct
		switch {
		case !ok:
			check.Gate = IdleCompactUsageUnknown
		case pct < cfg.CacheIdleCompactThresholdPercent:
			check.Gate = IdleCompactBelowThreshold
		default:
			check.Gate = IdleCompactReady
		}
	}
	return check
}

// tickIdleCompact は cache の失効前の 1 段目｡pending と cooldown は呼び出し元が見ている｡
func tickIdleCompact(
	ctx context.Context, deps Deps, pane multiplexer.Pane, ps *PaneState,
	cs *sessionstate.Compact, screen string, now time.Time,
) (Outcome, error) {
	cfg := deps.Config
	// cache の状態は transcript から求める｡伸び続ける working な pane では読まない｡
	if !canSendTo(deps, pane, screen) {
		return OutcomeMonitoring, nil
	}
	// 使用率は statusline の観測ではなく直近の応答から求める｡idle な pane は statusline が
	// 再描画されず､観測が窓の開く前に stateMaxAgeSeconds を過ぎるため｡
	check := EvaluateIdleCompact(cfg, cs, paneCache(deps, pane), now)
	switch {
	case check.Gate == IdleCompactShortTTL:
		// 5m TTL ではターンのたびに条件を満たすので､報告は pane ごとに 1 回にする｡
		if ps.IdleCompactShortTTLReported {
			return OutcomeMonitoring, nil
		}
		ps.IdleCompactShortTTLReported = true
		return OutcomeIdleCompactShortTTL, nil
	case check.Gate.Held():
		ps.IdleCompactHeld = check
		return OutcomeIdleCompactHeld, nil
	case check.Gate != IdleCompactReady:
		return OutcomeMonitoring, nil
	}

	ps.CompactPrepSentAt = now
	ps.CompactAutoSentAt = now
	ps.CompactPrepIdle = true
	if err := sendPrompt(ctx, deps, pane, ps, cfg.CompactAutoPrepMessage, now); err != nil {
		return OutcomeSendError, fmt.Errorf("pane %s への compact-prep 投入 (idle compact) に失敗: %w", pane.PaneID, err)
	}
	return OutcomeIdleCompactPrepSent, nil
}

// holdAfterIdleCompact は idle compact の marker が active な間､3 段目と画面判定を黙らせる｡
// 今ある圧縮完了 marker は処理済みとし､利用者が戻った後にこの圧縮で再開を送らない｡
// 画面判定の候補も捨てる｡
func holdAfterIdleCompact(ps *PaneState, cs *sessionstate.Compact) {
	if cs.HasCompacted && cs.CompactedAt.After(ps.CompactResumedAt) {
		ps.CompactResumedAt = cs.CompactedAt
	}
	ps.CompactSignature = ""
	ps.CompactSeenAt = time.Time{}
}

// paneCache は pane のセッションの cache の状態を返す｡無ければ nil｡
func paneCache(deps Deps, pane multiplexer.Pane) *sessionstate.Cache {
	if deps.CacheState == nil {
		return nil
	}
	return deps.CacheState(pane.SessionID)
}

func markIdleCompacted(deps Deps, pane multiplexer.Pane) error {
	if deps.MarkIdleCompacted == nil {
		return errors.New("marker の書き込み先が配線されていない")
	}
	return deps.MarkIdleCompacted(pane.SessionID)
}
