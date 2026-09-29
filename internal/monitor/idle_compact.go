package monitor

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/usadamasa/agents-daemon/internal/herdrcli"
	"github.com/usadamasa/agents-daemon/internal/sessionstate"
)

// idle compact は､idle な pane の prompt cache が失効する前に 1 段目・2 段目を走らせる｡
// /compact の前に cache-idle-compacted/<session_id> を書き､それが active な間は 3 段目と
// 画面判定を送らない｡理由は skills/agents-daemon/references/compact.md の「idle compact」｡

// shortCacheTTL 以下の TTL では prep と compact の 2 段を失効前に終える余裕が無い｡
const shortCacheTTL = 5 * time.Minute

// tickIdleCompact は cache の失効前の 1 段目｡pending と cooldown は呼び出し元が見ている｡
func tickIdleCompact(
	ctx context.Context, deps Deps, pane herdrcli.Pane, ps *PaneState,
	cs *sessionstate.Compact, screen string, now time.Time,
) (Outcome, error) {
	cfg := deps.Config
	maxAge := time.Duration(cfg.StateMaxAgeSeconds) * time.Second
	if !cs.ContextFresh(now, maxAge) || cs.UsedPercentage < cfg.CacheIdleCompactThresholdPercent {
		return OutcomeMonitoring, nil
	}
	// sidecar が古い (Stop が発火していない) と失効済みと見て送らない｡安全側なのでそのまま｡
	cache := paneCache(deps, pane)
	if cache == nil || cache.Expired(now) {
		return OutcomeMonitoring, nil
	}
	lead := time.Duration(cfg.CacheIdleCompactLeadSeconds) * time.Second
	if now.Before(cache.ExpiresAt().Add(-lead)) {
		return OutcomeMonitoring, nil
	}
	if !canSendTo(deps, pane, screen) {
		return OutcomeMonitoring, nil
	}
	if cache.TTL <= shortCacheTTL || cache.TTL <= lead {
		// 5m TTL ではターンのたびに条件を満たすので､報告は pane ごとに 1 回にする｡
		if ps.IdleCompactShortTTLReported {
			return OutcomeMonitoring, nil
		}
		ps.IdleCompactShortTTLReported = true
		return OutcomeIdleCompactShortTTL, nil
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

// paneCache は pane のセッションの cache の sidecar を返す｡無ければ nil｡
func paneCache(deps Deps, pane herdrcli.Pane) *sessionstate.Cache {
	if deps.CacheState == nil {
		return nil
	}
	return deps.CacheState(pane.SessionID())
}

func markIdleCompacted(deps Deps, pane herdrcli.Pane) error {
	if deps.MarkIdleCompacted == nil {
		return errors.New("marker の書き込み先が配線されていない")
	}
	return deps.MarkIdleCompacted(pane.SessionID())
}
