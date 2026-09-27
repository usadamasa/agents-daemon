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

// ackRecorder は Deps.CacheAck の差し替え｡書かれた値を残す｡
type ackRecorder struct {
	writes []string
	err    error
}

func (a *ackRecorder) write(_ string, value string) error {
	a.writes = append(a.writes, value)
	return a.err
}

// cacheDeps は cache の sidecar (nil なら無し) と ack の記録先を差した Deps を返す｡
func cacheDeps(t *testing.T, cfg config.Config, client *herdrclifake.Client, cache *sessionstate.Cache, ack *ackRecorder) Deps {
	t.Helper()
	deps := newTestDeps(t, cfg, client)
	deps.CacheState = func(string) *sessionstate.Cache { return cache }
	deps.CacheAck = ack.write
	return deps
}

func newTestPaneWithSession(status herdrcli.AgentStatus) herdrcli.Pane {
	pane := newTestPane(status, true)
	pane.AgentSession = &herdrcli.AgentSession{Agent: "claude", Kind: "id", Source: "herdr:claude", Value: "sess-1"}
	return pane
}

// cacheAt は last に始まった応答の 1h cache｡LastRequestRaw は transcript の文字列そのまま｡
func cacheAt(last time.Time) *sessionstate.Cache {
	return &sessionstate.Cache{
		LastRequestAt:  last,
		LastRequestRaw: last.Format("2006-01-02T15:04:05.000Z07:00"),
		TTL:            time.Hour,
	}
}

// retryTick は待機明けの pane へ retryMessage を送る 1 tick を回す｡
func retryTick(t *testing.T, deps Deps, ps *PaneState, now time.Time) (Outcome, error) {
	t.Helper()
	return Tick(context.Background(), deps, newTestPaneWithSession(herdrcli.AgentStatusIdle), ps, nil, now)
}

// sendTextRequiringAck は SendText の時点で ack が既に書かれていることを要求する｡
// ack は送信の前に書く｡後だと guard が先に prompt を見て止める｡
func sendTextRequiringAck(t *testing.T, ack *ackRecorder) func(context.Context, string, string) error {
	return func(context.Context, string, string) error {
		if len(ack.writes) == 0 {
			t.Error("ack を書く前に SendText が呼ばれた")
		}
		return nil
	}
}

func TestTick_失効後の非スラッシュ送信は_ack_を書いてから送る(t *testing.T) {
	now := time.Date(2026, 9, 27, 9, 0, 0, 0, time.UTC)
	ack := &ackRecorder{}
	client := &herdrclifake.Client{
		PaneReadFunc: readReturning(limitAndRelativeScreen, nil),
		SendTextFunc: sendTextRequiringAck(t, ack),
	}
	cache := cacheAt(now.Add(-5 * time.Hour)) // 利用上限からの再開は必ず失効後
	deps := cacheDeps(t, config.Default(), client, cache, ack)
	ps := &PaneState{Status: StatusWaiting}

	outcome, err := retryTick(t, deps, ps, now)
	if err != nil {
		t.Fatalf("Tick() error = %v", err)
	}
	if outcome != OutcomeRetried {
		t.Fatalf("outcome = %v, want %v", outcome, OutcomeRetried)
	}
	// 値は sidecar の文字列そのまま｡time.Parse / Format を通すとミリ秒や Z が変わり､
	// guard の文字列比較と合わなくなる｡
	if len(ack.writes) != 1 || ack.writes[0] != cache.LastRequestRaw {
		t.Errorf("ack = %v, want [%q]", ack.writes, cache.LastRequestRaw)
	}
	if ps.LastSend.Ack != cache.LastRequestRaw || ps.LastSend.Cache != cache || !ps.LastSend.At.Equal(now) {
		t.Errorf("LastSend = %+v, want ack=%q cache=sidecar at=%v", ps.LastSend, cache.LastRequestRaw, now)
	}
	if ps.LastSend.State() != "expired" {
		t.Errorf("LastSend.State() = %q, want expired", ps.LastSend.State())
	}
}

func TestTick_warm_なら_ack_を書かない(t *testing.T) {
	now := time.Date(2026, 9, 27, 9, 0, 0, 0, time.UTC)
	client := &herdrclifake.Client{PaneReadFunc: readReturning(transientScreen, nil)}
	ack := &ackRecorder{}
	cfg := config.Default()
	deps := cacheDeps(t, cfg, client, cacheAt(now.Add(-3*time.Minute)), ack)
	// transient の再送は短い間隔で回るので warm のまま送ることがある｡
	ps := &PaneState{Status: StatusWaiting}

	outcome, err := retryTick(t, deps, ps, now)
	if err != nil {
		t.Fatalf("Tick() error = %v", err)
	}
	if outcome != OutcomeRetried {
		t.Fatalf("outcome = %v, want %v", outcome, OutcomeRetried)
	}
	if len(ack.writes) != 0 {
		t.Errorf("warm なのに ack を書いた: %v", ack.writes)
	}
	if ps.LastSend.State() != "warm" || ps.LastSend.Ack != "" {
		t.Errorf("LastSend = %+v, want warm / ack なし", ps.LastSend)
	}
}

func TestTick_sidecar_が無ければ_sentinel_を書く(t *testing.T) {
	// sidecar が無い (Stop hook が入っていない､初回ターン) ときも何かを ack に残す｡
	// 残さないと guard が daemon の再開を止め､daemon は pane の停止と見て nudge を打ち､
	// それも止められて堂々巡りになる｡
	now := time.Date(2026, 9, 27, 9, 0, 0, 0, time.UTC)
	client := &herdrclifake.Client{PaneReadFunc: readReturning(limitAndRelativeScreen, nil)}
	ack := &ackRecorder{}
	deps := cacheDeps(t, config.Default(), client, nil, ack)
	ps := &PaneState{Status: StatusWaiting}

	if _, err := retryTick(t, deps, ps, now); err != nil {
		t.Fatalf("Tick() error = %v", err)
	}
	if len(ack.writes) != 1 || ack.writes[0] != sessionstate.CacheAckUnknown {
		t.Errorf("ack = %v, want [%q]", ack.writes, sessionstate.CacheAckUnknown)
	}
	if ps.LastSend.State() != "unknown" {
		t.Errorf("LastSend.State() = %q, want unknown", ps.LastSend.State())
	}
}

func TestTick_スラッシュで始まる_prompt_は失効後でも_ack_を書かない(t *testing.T) {
	// guard はスラッシュを素通しするので ack は要らない｡判定は config のキーではなく
	// 送る文面で行う (どの文面も設定で書き換えられる)｡
	now := time.Date(2026, 9, 27, 9, 0, 0, 0, time.UTC)
	client := &herdrclifake.Client{PaneReadFunc: readReturning(idleScreen, nil)}
	ack := &ackRecorder{}
	cfg := compactAutoConfig()
	deps := cacheDeps(t, cfg, client, cacheAt(now.Add(-5*time.Hour)), ack)
	deps.CompactState = func(string) *sessionstate.Compact { return freshContext(62, now) }
	ps := &PaneState{}

	outcome, err := Tick(context.Background(), deps, newTestPaneWithSession(herdrcli.AgentStatusIdle), ps, nil, now)
	if err != nil {
		t.Fatalf("Tick() error = %v", err)
	}
	if outcome != OutcomeCompactPrepSent {
		t.Fatalf("outcome = %v, want %v", outcome, OutcomeCompactPrepSent)
	}
	if len(ack.writes) != 0 {
		t.Errorf("スラッシュなのに ack を書いた: %v", ack.writes)
	}
	if !ps.LastSend.Slash || ps.LastSend.State() != "expired" {
		t.Errorf("LastSend = %+v, want slash / expired (ログには残す)", ps.LastSend)
	}
}

func TestTick_compact停止の継続要求も失効後は_ack_を書く(t *testing.T) {
	// sendPrompt 経路 (compactStallMessage) も recoverPane と同じ前処理を通る｡
	now := time.Date(2026, 9, 27, 9, 0, 0, 0, time.UTC)
	ack := &ackRecorder{}
	client := &herdrclifake.Client{
		PaneReadFunc: readReturning(compactStalledScreen, nil),
		SendTextFunc: sendTextRequiringAck(t, ack),
	}
	cfg := config.Default()
	cache := cacheAt(now.Add(-2 * time.Hour))
	deps := cacheDeps(t, cfg, client, cache, ack)
	pane := newTestPaneWithSession(herdrcli.AgentStatusIdle)
	ps := &PaneState{}

	if _, err := Tick(context.Background(), deps, pane, ps, nil, now); err != nil {
		t.Fatalf("Tick() (1 回目) error = %v", err)
	}
	quiet := time.Duration(cfg.CompactStallQuietSeconds) * time.Second
	outcome, err := Tick(context.Background(), deps, pane, ps, nil, now.Add(quiet+time.Second))
	if err != nil {
		t.Fatalf("Tick() (2 回目) error = %v", err)
	}
	if outcome != OutcomeCompactNudged {
		t.Fatalf("outcome = %v, want %v", outcome, OutcomeCompactNudged)
	}
	if len(ack.writes) != 1 || ack.writes[0] != cache.LastRequestRaw {
		t.Errorf("ack = %v, want [%q]", ack.writes, cache.LastRequestRaw)
	}
}

func TestTick_ack_の書き込みに失敗しても送信は止めない(t *testing.T) {
	// 足すのは ack とログだけで､送る・送らないの判断は変えない｡失敗は報告に残す｡
	now := time.Date(2026, 9, 27, 9, 0, 0, 0, time.UTC)
	client := &herdrclifake.Client{PaneReadFunc: readReturning(limitAndRelativeScreen, nil)}
	ack := &ackRecorder{err: errors.New("disk full")}
	deps := cacheDeps(t, config.Default(), client, cacheAt(now.Add(-5*time.Hour)), ack)
	ps := &PaneState{Status: StatusWaiting}

	outcome, err := retryTick(t, deps, ps, now)
	if err != nil {
		t.Fatalf("Tick() error = %v", err)
	}
	if outcome != OutcomeRetried {
		t.Fatalf("outcome = %v, want %v", outcome, OutcomeRetried)
	}
	assertSentText(t, client, config.Default().RetryMessage)
	if ps.LastSend.AckErr == nil {
		t.Error("LastSend.AckErr = nil, want 書き込みの失敗")
	}
}

func TestTick_session_が無い_pane_は_ack_を書けない(t *testing.T) {
	now := time.Date(2026, 9, 27, 9, 0, 0, 0, time.UTC)
	client := &herdrclifake.Client{PaneReadFunc: readReturning(limitAndRelativeScreen, nil)}
	ack := &ackRecorder{}
	deps := cacheDeps(t, config.Default(), client, cacheAt(now.Add(-5*time.Hour)), ack)
	ps := &PaneState{Status: StatusWaiting}

	if _, err := Tick(context.Background(), deps, newTestPane(herdrcli.AgentStatusIdle, true), ps, nil, now); err != nil {
		t.Fatalf("Tick() error = %v", err)
	}
	if len(ack.writes) != 0 {
		t.Errorf("session が無いのに ack を書いた: %v", ack.writes)
	}
	if ps.LastSend.State() != "unknown" {
		t.Errorf("LastSend.State() = %q, want unknown", ps.LastSend.State())
	}
}

func TestTick_loader_が無ければ_ack_も書かない(t *testing.T) {
	// テストや verify のように cache の配線を持たない呼び出し元｡
	now := time.Date(2026, 9, 27, 9, 0, 0, 0, time.UTC)
	client := &herdrclifake.Client{PaneReadFunc: readReturning(limitAndRelativeScreen, nil)}
	deps := newTestDeps(t, config.Default(), client)
	ps := &PaneState{Status: StatusWaiting}

	outcome, err := retryTick(t, deps, ps, now)
	if err != nil {
		t.Fatalf("Tick() error = %v", err)
	}
	if outcome != OutcomeRetried {
		t.Errorf("outcome = %v, want %v", outcome, OutcomeRetried)
	}
}
