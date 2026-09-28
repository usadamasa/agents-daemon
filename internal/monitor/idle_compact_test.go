package monitor

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/usadamasa/agents-daemon/internal/config"
	"github.com/usadamasa/agents-daemon/internal/herdrcli"
	"github.com/usadamasa/agents-daemon/internal/herdrcli/herdrclifake"
	"github.com/usadamasa/agents-daemon/internal/sessionstate"
)

// idleNow は idle compact のテストの基準時刻｡
var idleNow = time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)

// idleCompactConfig は idle compact だけを有効にした設定を返す｡compactAutoEnabled は既定の
// false のまま (独立して有効にできることを前提にする)｡
func idleCompactConfig() config.Config {
	cfg := config.Default()
	cfg.CacheIdleCompactEnabled = true
	// 各テストの使用率 45 はこの閾値に対する値｡既定値の変更に引きずられないよう固定する｡
	cfg.CacheIdleCompactThresholdPercent = 40
	cfg.CacheIdleCompactLeadSeconds = 600
	return cfg
}

// markRecorder は Deps.MarkIdleCompacted の差し替え｡
type markRecorder struct {
	marks int
	err   error
}

func (m *markRecorder) mark(string) error {
	m.marks++
	return m.err
}

// idleFixture は idle compact の 1 tick に要る入力｡
type idleFixture struct {
	cfg    config.Config
	client *herdrclifake.Client
	ps     *PaneState
	cs     *sessionstate.Compact
	cache  *sessionstate.Cache
	marker *markRecorder
}

// newIdleFixture は発火条件をすべて満たす入力を返す｡1h TTL の cache が失効の 5 分前
// (lead 600 秒の窓の中)､使用率 45%､入力欄が空の idle な pane｡
func newIdleFixture() *idleFixture {
	return &idleFixture{
		cfg:    idleCompactConfig(),
		client: &herdrclifake.Client{PaneReadFunc: readReturning(idleScreen, nil)},
		ps:     &PaneState{},
		cs:     freshContext(45, idleNow),
		cache:  cacheAt(idleNow.Add(-55 * time.Minute)),
		marker: &markRecorder{},
	}
}

func (f *idleFixture) tick(t *testing.T, now time.Time) (Outcome, error) {
	t.Helper()
	deps := newTestDeps(t, f.cfg, f.client)
	deps.CompactState = func(string) *sessionstate.Compact { return f.cs }
	deps.CacheState = func(string) *sessionstate.Cache { return f.cache }
	deps.MarkIdleCompacted = f.marker.mark
	return Tick(context.Background(), deps, newTestPaneWithSession(herdrcli.AgentStatusIdle), f.ps, nil, now)
}

func (f *idleFixture) mustTick(t *testing.T, now time.Time) Outcome {
	t.Helper()
	outcome, err := f.tick(t, now)
	if err != nil {
		t.Fatalf("Tick() error = %v", err)
	}
	return outcome
}

func TestTick_idle_compact_は失効が近づいたら_compact_prep_を送る(t *testing.T) {
	f := newIdleFixture()

	outcome := f.mustTick(t, idleNow)

	if outcome != OutcomeIdleCompactPrepSent {
		t.Fatalf("outcome = %v, want %v", outcome, OutcomeIdleCompactPrepSent)
	}
	assertSentText(t, f.client, f.cfg.CompactAutoPrepMessage)
	if !f.ps.CompactPrepIdle || f.ps.CompactPrepSentAt != idleNow {
		t.Errorf("PaneState = %+v, want idle 起点の pending", f.ps)
	}
}

func TestTick_idle_compact_は送らない(t *testing.T) {
	for _, tt := range []struct {
		name  string
		setup func(f *idleFixture)
	}{
		{"cacheIdleCompactEnabled が false", func(f *idleFixture) { f.cfg.CacheIdleCompactEnabled = false }},
		{"失効後", func(f *idleFixture) { f.cache = cacheAt(idleNow.Add(-61 * time.Minute)) }},
		{"窓が開く前", func(f *idleFixture) { f.cache = cacheAt(idleNow.Add(-30 * time.Minute)) }},
		{"sidecar が無い", func(f *idleFixture) { f.cache = nil }},
		{"使用率が閾値未満", func(f *idleFixture) { f.cs = freshContext(39, idleNow) }},
		{"使用率の観測が古い", func(f *idleFixture) {
			f.cs = freshContext(45, idleNow.Add(-time.Duration(f.cfg.StateMaxAgeSeconds+1)*time.Second))
		}},
		{"cooldown 中", func(f *idleFixture) { f.ps.CompactAutoSentAt = idleNow.Add(-5 * time.Minute) }},
		{"入力欄に書きかけがある", func(f *idleFixture) { f.client.PaneReadFunc = readReturning(typingScreen, nil) }},
		// 前回の idle compact から利用者が戻っていない｡圧縮後の使用率は下がるので普通は閾値で止まる｡
		{"idle compact の marker が active", func(f *idleFixture) {
			f.cs.HasIdleCompacted = true
			f.cs.IdleCompactedAt = idleNow.Add(-50 * time.Minute)
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := newIdleFixture()
			tt.setup(f)

			if outcome := f.mustTick(t, idleNow); outcome != OutcomeMonitoring {
				t.Errorf("outcome = %v, want %v", outcome, OutcomeMonitoring)
			}
			assertNoSend(t, f.client)
		})
	}
}

func TestTick_idle_compact_は_5m_TTL_ではスキップする(t *testing.T) {
	// 5 分では prep と compact の 2 段を失効前に終えられない｡
	f := newIdleFixture()
	f.cache = cacheAt(idleNow.Add(-2 * time.Minute))
	f.cache.TTL = 5 * time.Minute

	if outcome := f.mustTick(t, idleNow); outcome != OutcomeIdleCompactShortTTL {
		t.Fatalf("outcome = %v, want %v", outcome, OutcomeIdleCompactShortTTL)
	}
	assertNoSend(t, f.client)
}

func TestTick_5m_TTL_のスキップは圧縮後の再開を塞がない(t *testing.T) {
	f := newIdleFixture()
	f.cache = cacheAt(idleNow.Add(-2 * time.Minute))
	f.cache.TTL = 5 * time.Minute
	f.cs.HasCompacted = true
	f.cs.CompactedAt = idleNow.Add(-time.Duration(f.cfg.CompactResumeDelaySeconds+1) * time.Second)

	if outcome := f.mustTick(t, idleNow); outcome != OutcomeCompactResumed {
		t.Fatalf("outcome = %v, want %v", outcome, OutcomeCompactResumed)
	}
}

// prepWritten は prep を送った後に state file が書かれた状態へ進める｡
func (f *idleFixture) prepWritten(sentAt, writtenAt time.Time) {
	f.ps.CompactPrepSentAt = sentAt
	f.ps.CompactAutoSentAt = sentAt
	f.ps.CompactPrepIdle = true
	f.cs.HasPrep = true
	f.cs.PrepWrittenAt = writtenAt
	// prep のターン自体が cache を更新する (応答の開始は state file の書き込みより前)｡
	f.cache = cacheAt(sentAt.Add(time.Second))
}

func TestTick_idle_compact_は_marker_を書いてから_compact_を送る(t *testing.T) {
	// compactAutoEnabled が false でも､idle 起点の pending は畳まれない｡
	f := newIdleFixture()
	f.prepWritten(idleNow, idleNow.Add(35*time.Second))
	f.client.SendTextFunc = func(context.Context, string, string) error {
		if f.marker.marks == 0 {
			t.Error("marker を書く前に /compact を送った")
		}
		return nil
	}
	later := idleNow.Add(40 * time.Second)

	outcome := f.mustTick(t, later)

	if outcome != OutcomeIdleCompactSent {
		t.Fatalf("outcome = %v, want %v", outcome, OutcomeIdleCompactSent)
	}
	assertSentText(t, f.client, f.cfg.CompactAutoMessage)
	if f.marker.marks != 1 {
		t.Errorf("marker を書いた回数 = %d, want 1", f.marker.marks)
	}
	if !f.ps.CompactPrepSentAt.IsZero() || f.ps.CompactPrepIdle {
		t.Errorf("pending が畳まれていない: %+v", f.ps)
	}
}

func TestTick_idle_compact_は_marker_を書けなければ_compact_を送らない(t *testing.T) {
	// marker が無いと､圧縮後に再開の nudge が不在の利用者へ届く｡
	f := newIdleFixture()
	f.prepWritten(idleNow, idleNow.Add(35*time.Second))
	f.marker.err = errors.New("disk full")

	outcome, err := f.tick(t, idleNow.Add(40*time.Second))

	if err == nil || outcome != OutcomeSendError {
		t.Fatalf("outcome, err = %v, %v, want %v と error", outcome, err, OutcomeSendError)
	}
	assertNoSend(t, f.client)
	if !f.ps.CompactPrepSentAt.IsZero() {
		t.Errorf("pending が畳まれていない (毎 tick 書き込みを試し続ける): %+v", f.ps)
	}
}

func TestTick_idle_compact_は_prep_の後に利用者が戻っていたら中止する(t *testing.T) {
	// /compact は取り消せない｡戻ってきた利用者の前で細部を落とさない｡
	f := newIdleFixture()
	f.prepWritten(idleNow, idleNow.Add(35*time.Second))
	f.cache = cacheAt(idleNow.Add(2 * time.Minute))

	outcome := f.mustTick(t, idleNow.Add(3*time.Minute))

	if outcome != OutcomeIdleCompactAborted {
		t.Fatalf("outcome = %v, want %v", outcome, OutcomeIdleCompactAborted)
	}
	assertNoSend(t, f.client)
	if f.marker.marks != 0 {
		t.Errorf("中止したのに marker を書いた")
	}
	if !f.ps.CompactPrepSentAt.IsZero() || f.ps.CompactPrepIdle {
		t.Errorf("pending が畳まれていない: %+v", f.ps)
	}
}

func TestTick_idle_compact_の後は再開を促さない(t *testing.T) {
	// 利用者は離席中｡再開の nudge はそれ自体が失効後の送信になり､誰もいないのに作業が進む｡
	f := newIdleFixture()
	f.cs = freshContext(8, idleNow)
	f.cs.HasIdleCompacted = true
	f.cs.IdleCompactedAt = idleNow.Add(-10 * time.Minute)
	f.cs.HasCompacted = true
	f.cs.CompactedAt = idleNow.Add(-9 * time.Minute)
	// compact の後に Stop が発火し､境界の後ろに応答が無いので sidecar は消えている｡
	f.cache = nil

	if outcome := f.mustTick(t, idleNow); outcome != OutcomeMonitoring {
		t.Fatalf("outcome = %v, want %v", outcome, OutcomeMonitoring)
	}
	assertNoSend(t, f.client)

	// daemon が再起動して PaneState を失っても同じ (marker はファイル)｡
	f.ps = &PaneState{}
	if outcome := f.mustTick(t, idleNow.Add(3*time.Hour)); outcome != OutcomeMonitoring {
		t.Fatalf("再起動後: outcome = %v, want %v", outcome, OutcomeMonitoring)
	}
	assertNoSend(t, f.client)
}

func TestTick_利用者が戻った後の圧縮は通常どおり再開を促す(t *testing.T) {
	f := newIdleFixture()
	f.cs = freshContext(8, idleNow)
	f.cs.HasIdleCompacted = true
	f.cs.IdleCompactedAt = idleNow.Add(-3 * time.Hour)
	f.cs.HasCompacted = true
	f.cs.CompactedAt = idleNow.Add(-2 * time.Minute)
	f.cache = cacheAt(idleNow.Add(-5 * time.Minute))

	if outcome := f.mustTick(t, idleNow); outcome != OutcomeCompactResumed {
		t.Fatalf("outcome = %v, want %v", outcome, OutcomeCompactResumed)
	}
}

func TestTick_idle_compact_の後は画面判定の継続要求も送らない(t *testing.T) {
	f := newIdleFixture()
	f.client.PaneReadFunc = readReturning(compactStalledScreen, nil)
	f.cs = freshContext(4, idleNow)
	f.cs.HasIdleCompacted = true
	f.cs.IdleCompactedAt = idleNow.Add(-time.Minute)
	f.cache = nil

	if outcome := f.mustTick(t, idleNow); outcome != OutcomeMonitoring {
		t.Fatalf("1 回目: outcome = %v, want %v", outcome, OutcomeMonitoring)
	}
	quiet := time.Duration(f.cfg.CompactStallQuietSeconds) * time.Second
	if outcome := f.mustTick(t, idleNow.Add(quiet+time.Second)); outcome != OutcomeMonitoring {
		t.Fatalf("2 回目: outcome = %v, want %v", outcome, OutcomeMonitoring)
	}
	assertNoSend(t, f.client)
}
