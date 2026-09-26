package monitor

import (
	"context"
	"testing"
	"time"

	"github.com/usadamasa/agents-daemon/internal/config"
	"github.com/usadamasa/agents-daemon/internal/herdrcli"
	"github.com/usadamasa/agents-daemon/internal/herdrcli/herdrclifake"
)

const (
	// nativeArmedScreen は Claude Code 自身の auto-continue がカウントダウン中の画面。
	// 上限に当たった元の行も残っているので、二重シグナルは揃っている。
	nativeArmedScreen = "⏺ You've hit your session limit · resets 5:00pm (Asia/Tokyo)\n\n" +
		"Usage limit reached · continuing automatically at 5:00pm · esc or type to cancel"
	// resumeReadyScreen は解除済みで Enter だけを待っている画面。
	resumeReadyScreen = "Usage limit has reset · press enter to continue"
	// hardCapScreen は待っても解除されない上限。2.1.239 でリセット時刻が併記される
	// ようになり、二重シグナル要件を満たしてしまう。
	hardCapScreen = "⏺ You've hit your monthly spend limit · Your session limit resets 5:00pm"
)

func TestTick_ネイティブauto_continue中は送信も待機もしない(t *testing.T) {
	client := &herdrclifake.Client{PaneReadFunc: readReturning(nativeArmedScreen, nil)}
	deps := newTestDeps(t, config.Default(), client)
	pane := newTestPane(herdrcli.AgentStatusWorking, true)
	ps := &PaneState{}

	outcome, err := Tick(context.Background(), deps, pane, ps, nil, time.Now())
	if err != nil {
		t.Fatalf("Tick() error = %v", err)
	}
	if outcome != OutcomeNativeAutoContinue {
		t.Errorf("Outcome = %v, want %v", outcome, OutcomeNativeAutoContinue)
	}
	if ps.Status != StatusMonitoring {
		t.Errorf("Status = %v, want %v (ネイティブに任せる間は待機に入らない)", ps.Status, StatusMonitoring)
	}
	assertNoSend(t, client)
}

func TestTick_待機中にネイティブが再度予約したら送らず監視へ戻る(t *testing.T) {
	// ここで Escape を送るとネイティブの継続そのものが取り消される
	// ("esc or type to cancel")。起床時刻に達していても手を出さない。
	client := &herdrclifake.Client{PaneReadFunc: readReturning(nativeArmedScreen, nil)}
	deps := newTestDeps(t, config.Default(), client)
	pane := newTestPane(herdrcli.AgentStatusWorking, true)
	now := time.Now()
	ps := &PaneState{Status: StatusWaiting, WaitUntil: now.Add(-time.Second), Attempts: 2}

	outcome, err := Tick(context.Background(), deps, pane, ps, nil, now)
	if err != nil {
		t.Fatalf("Tick() error = %v", err)
	}
	if outcome != OutcomeNativeAutoContinue {
		t.Errorf("Outcome = %v, want %v", outcome, OutcomeNativeAutoContinue)
	}
	if ps.Status != StatusMonitoring || ps.Attempts != 0 {
		t.Errorf("Status = %v Attempts = %d, want monitoring / 0", ps.Status, ps.Attempts)
	}
	assertNoSend(t, client)
}

func TestTick_deferToNativeAutoContinueをoffにすると自前で待機する(t *testing.T) {
	cfg := config.Default()
	cfg.DeferToNativeAutoContinue = false

	client := &herdrclifake.Client{PaneReadFunc: readReturning(nativeArmedScreen, nil)}
	deps := newTestDeps(t, cfg, client)
	pane := newTestPane(herdrcli.AgentStatusWorking, true)
	ps := &PaneState{}

	outcome, err := Tick(context.Background(), deps, pane, ps, nil, time.Now())
	if err != nil {
		t.Fatalf("Tick() error = %v", err)
	}
	if outcome != OutcomeWaiting || ps.Status != StatusWaiting {
		t.Errorf("Outcome = %v Status = %v, want waiting", outcome, ps.Status)
	}
}

func TestTick_enter待ちにはenterだけを送る(t *testing.T) {
	var sentKeys []string
	client := &herdrclifake.Client{
		PaneReadFunc: readReturning(resumeReadyScreen, nil),
		SendKeysFunc: func(_ context.Context, _ string, keys ...string) error {
			sentKeys = append(sentKeys, keys...)
			return nil
		},
		SendTextFunc: func(context.Context, string, string) error {
			t.Error("SendText が呼ばれた: テキストを打つと中断された turn ではなく新しい依頼になる")
			return nil
		},
	}
	deps := newTestDeps(t, config.Default(), client)
	pane := newTestPane(herdrcli.AgentStatusIdle, true)
	ps := &PaneState{}
	now := time.Now()

	// 通常の上限と同じ状態機械に乗せる。待つ理由は無いので即座に起床時刻になる。
	outcome, err := Tick(context.Background(), deps, pane, ps, nil, now)
	if err != nil {
		t.Fatalf("Tick() error = %v", err)
	}
	if outcome != OutcomeWaiting || ps.Status != StatusWaiting {
		t.Fatalf("Outcome = %v Status = %v, want waiting", outcome, ps.Status)
	}
	if ps.WaitUntil.After(now) {
		t.Errorf("WaitUntil = %v, want %v 以前 (解除済みなので待つ理由が無い)", ps.WaitUntil, now)
	}

	outcome, err = Tick(context.Background(), deps, pane, ps, nil, now)
	if err != nil {
		t.Fatalf("Tick() error = %v", err)
	}
	if outcome != OutcomeResumeEnter {
		t.Errorf("Outcome = %v, want %v", outcome, OutcomeResumeEnter)
	}
	if len(sentKeys) != 1 || sentKeys[0] != "enter" {
		t.Errorf("送ったキー = %v, want [enter] (esc はネイティブの取り消しキー)", sentKeys)
	}
	if ps.Attempts != 1 {
		t.Errorf("Attempts = %d, want 1 (効かなかったときに maxRetries で打ち切るため)", ps.Attempts)
	}
}

func TestTick_enterが効かないままなら最終的に打ち切る(t *testing.T) {
	// Enter を押しても画面が変わらないことがある。ここで数え上げが効かないと
	// 30 秒おきに永久に Enter を押し続ける pane ができる。
	client := &herdrclifake.Client{PaneReadFunc: readReturning(resumeReadyScreen, nil)}
	cfg := config.Default()
	deps := newTestDeps(t, cfg, client)
	pane := newTestPane(herdrcli.AgentStatusIdle, true)
	ps := &PaneState{}
	now := time.Now()

	for range cfg.MaxRetries + 5 {
		outcome, err := Tick(context.Background(), deps, pane, ps, nil, now)
		if err != nil {
			t.Fatalf("Tick() error = %v", err)
		}
		if outcome == OutcomeMaxRetries {
			return
		}
		now = now.Add(time.Hour)
	}
	t.Errorf("maxRetries=%d を超えても打ち切られなかった (Attempts=%d)", cfg.MaxRetries, ps.Attempts)
}

func TestTick_恒久的な上限では待機に入らない(t *testing.T) {
	client := &herdrclifake.Client{PaneReadFunc: readReturning(hardCapScreen, nil)}
	deps := newTestDeps(t, config.Default(), client)
	pane := newTestPane(herdrcli.AgentStatusIdle, true)
	ps := &PaneState{}

	outcome, err := Tick(context.Background(), deps, pane, ps, nil, time.Now())
	if err != nil {
		t.Fatalf("Tick() error = %v", err)
	}
	if outcome != OutcomeHardCap {
		t.Errorf("Outcome = %v, want %v", outcome, OutcomeHardCap)
	}
	if ps.Status != StatusMonitoring {
		t.Errorf("Status = %v, want monitoring (待っても解除されない上限)", ps.Status)
	}
	assertNoSend(t, client)
}

func assertNoSend(t *testing.T, client *herdrclifake.Client) {
	t.Helper()
	for _, call := range client.Calls {
		if call == "SendText:pane-1" || call == "SendKeys:pane-1" {
			t.Errorf("送信が発生した: %v", client.Calls)
			return
		}
	}
}
