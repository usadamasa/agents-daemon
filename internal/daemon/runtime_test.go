package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/usadamasa/agents-daemon/internal/applog"
	"github.com/usadamasa/agents-daemon/internal/herdrcli"
	"github.com/usadamasa/agents-daemon/internal/herdrcli/herdrclifake"
	"github.com/usadamasa/agents-daemon/internal/monitor"
	"github.com/usadamasa/agents-daemon/internal/sessionstate"
)

// newTestRuntime は daemonRuntime を t.TempDir() の下だけで完結させて組み立てる。
// statusPath はログディレクトリと同じ一時ディレクトリ内に置く。
func newTestRuntime(t *testing.T, client herdrcli.Client, startedAt time.Time) (*daemonRuntime, string) {
	t.Helper()
	dir := t.TempDir()
	log := applog.NewLogger(filepath.Join(dir, "daemon.log"), time.Now)
	t.Cleanup(func() { _ = log.Close() })
	statusPath := filepath.Join(dir, "status.json")
	// sidecar file のディレクトリは一時ディレクトリ配下を指すだけで作らない。
	// 存在しないディレクトリは「state 無し」として扱われ、compact の段は
	// 何もしない (ここで検証したいのは上限周りの遷移なので)。
	// 利用上限 state が要るテストは r.store.RateLimits へ書く。
	return newDaemonRuntime(client, log, statusPath, sessionstate.New(dir), func(time.Duration) {}, startedAt), statusPath
}

func claudePane(paneID, terminalID string, status herdrcli.AgentStatus) herdrcli.Pane {
	return herdrcli.Pane{
		Agent:       "claude-code",
		AgentStatus: status,
		PaneID:      paneID,
		TerminalID:  terminalID,
		CWD:         "/tmp/project",
	}
}

func nonClaudePane(paneID, terminalID string) herdrcli.Pane {
	return herdrcli.Pane{
		Agent:       "bash",
		AgentStatus: herdrcli.AgentStatusIdle,
		PaneID:      paneID,
		TerminalID:  terminalID,
	}
}

// missingConfigPath は「まだファイルが無い」正常系を意図的に使う場合の共通パス。
// config.Load は os.ErrNotExist をエラー扱いしない。
func missingConfigPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "config.json")
}

func TestDaemonRuntimeTick_NewPaneGetsFreshState(t *testing.T) {
	client := &herdrclifake.Client{
		PaneListFunc: func(context.Context) ([]herdrcli.Pane, error) {
			return []herdrcli.Pane{claudePane("p1", "t1", herdrcli.AgentStatusIdle)}, nil
		},
	}
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	r, _ := newTestRuntime(t, client, now)

	shutdown, _, err := r.tick(context.Background(), missingConfigPath(t), now)
	if err != nil {
		t.Fatalf("tick() error = %v", err)
	}
	if shutdown {
		t.Fatal("tick() shutdown = true, want false")
	}

	if len(r.panes) != 1 {
		t.Fatalf("len(r.panes) = %d, want 1", len(r.panes))
	}
	entry, ok := r.panes["t1"]
	if !ok {
		t.Fatal("terminal_id t1 のエントリが作られていない")
	}
	if entry.state == nil {
		t.Fatal("entry.state が nil (フレッシュな状態機械が割り当てられていない)")
	}
	if entry.state.Status != monitor.StatusMonitoring || entry.state.Attempts != 0 {
		t.Errorf("entry.state = %+v, want ゼロ値 (Monitoring, Attempts=0)", entry.state)
	}
	if !entry.lastSeenAt.Equal(now) {
		t.Errorf("entry.lastSeenAt = %v, want %v", entry.lastSeenAt, now)
	}
	if entry.pane.PaneID != "p1" {
		t.Errorf("entry.pane.PaneID = %q, want %q", entry.pane.PaneID, "p1")
	}
}

func TestDaemonRuntimeTick_DisappearedPaneEntryDropped(t *testing.T) {
	var panesNow []herdrcli.Pane
	client := &herdrclifake.Client{
		PaneListFunc: func(context.Context) ([]herdrcli.Pane, error) {
			return panesNow, nil
		},
	}
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	r, _ := newTestRuntime(t, client, now)
	cfgPath := missingConfigPath(t)

	panesNow = []herdrcli.Pane{claudePane("p1", "t1", herdrcli.AgentStatusIdle)}
	if _, _, err := r.tick(context.Background(), cfgPath, now); err != nil {
		t.Fatalf("1回目の tick() error = %v", err)
	}
	if len(r.panes) != 1 {
		t.Fatalf("1回目の tick 後 len(r.panes) = %d, want 1", len(r.panes))
	}

	panesNow = nil
	if _, _, err := r.tick(context.Background(), cfgPath, now.Add(time.Second)); err != nil {
		t.Fatalf("2回目の tick() error = %v", err)
	}
	if len(r.panes) != 0 {
		t.Fatalf("pane が消えた後も r.panes に残っている: %v (map が無制限に育ってしまう)", r.panes)
	}
}

func TestDaemonRuntimeTick_NonClaudePaneNeverGetsEntry(t *testing.T) {
	client := &herdrclifake.Client{
		PaneListFunc: func(context.Context) ([]herdrcli.Pane, error) {
			return []herdrcli.Pane{nonClaudePane("p1", "t1")}, nil
		},
	}
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	r, _ := newTestRuntime(t, client, now)

	if _, _, err := r.tick(context.Background(), missingConfigPath(t), now); err != nil {
		t.Fatalf("tick() error = %v", err)
	}
	if len(r.panes) != 0 {
		t.Errorf("non-Claude pane にエントリが作られた: %v", r.panes)
	}
	// PaneRead 等が呼ばれていれば、non-Claude pane を素通りできていない証拠になる。
	if len(client.Calls) != 1 || client.Calls[0] != "PaneList" {
		t.Errorf("client.Calls = %v, want [PaneList] のみ (non-Claude pane は読みに行かない)", client.Calls)
	}
}

// TestDaemonRuntimeTick_RateStateLoadedOnceIsSharedAcrossPanes は、1 tick 内で
// rate-limits.json を読み込むのが 1 回だけであり、その結果 (*limitstate.State) が
// 全 pane の monitor.Tick 呼び出しへそのまま使い回されることを検証する。
//
// pane 単位で毎回読み直す実装だと、1 pane 目の処理中にファイルが書き換わった
// (あるいは消えた) 場合、同じ tick 内の 2 pane 目が別の瞬間の state を見てしまう。
// これを検出するため、1 pane 目の PaneRead 呼び出し (screen 読み取り自体が
// tick 処理の一部) の副作用として rate-limits.json を削除し、2 pane 目の判定が
// 削除前の値のまま (= 1回だけ読み込んだ値を使い回している) であることを確認する。
// TestDaemonRuntimeTick_MissingSessionStateDoesNotLeakToOtherPanes は、片方の
// セッションの state が無い (statusline がまだ書いていない) ときに、もう片方の判定が
// 巻き込まれないことを確かめる。
//
// state をセッションごとに分ける前は 1 ファイルを全 pane で共有していたため、
// 片方の事情がもう片方の判定を左右した。
func TestDaemonRuntimeTick_MissingSessionStateDoesNotLeakToOtherPanes(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	const limitScreen = "Try again in 5 minutes"

	paneA := claudePaneWithSession("pane-a", "term-a", "session-a", herdrcli.AgentStatusIdle)
	paneB := claudePaneWithSession("pane-b", "term-b", "session-b", herdrcli.AgentStatusIdle)

	client := &herdrclifake.Client{
		PaneListFunc: func(context.Context) ([]herdrcli.Pane, error) {
			return []herdrcli.Pane{paneA, paneB}, nil
		},
		PaneReadFunc: func(context.Context, string, herdrcli.PaneReadOptions) (string, error) {
			return limitScreen, nil
		},
	}

	r, _ := newTestRuntime(t, client, now)
	// session-a の state だけ書く。session-b には対応するファイルが無い。
	writeSessionRateState(t, r.store.RateLimits, "session-a", 10, now, now.Add(time.Hour))

	if _, _, err := r.tick(context.Background(), missingConfigPath(t), now); err != nil {
		t.Fatalf("tick() error = %v", err)
	}

	if got := r.panes["term-a"].state.Status; got != monitor.StatusMonitoring {
		t.Errorf("pane-a の state.Status = %v, want Monitoring (自分の state が 10%% なのでゲートで抑制されるべき)", got)
	}
	if got := r.panes["term-b"].state.Status; got != monitor.StatusWaiting {
		t.Errorf("pane-b の state.Status = %v, want Waiting (state が無いならゲートを適用せず画面の判定に従うべき)", got)
	}
}

func TestDaemonRuntimeTick_IdleShutdown(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(cfgPath, []byte(`{"idleShutdownMinutes": 10}`), 0o644); err != nil {
		t.Fatalf("設定ファイルの書き込みに失敗: %v", err)
	}

	var panesNow []herdrcli.Pane
	client := &herdrclifake.Client{
		PaneListFunc: func(context.Context) ([]herdrcli.Pane, error) { return panesNow, nil },
	}
	t0 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	r, _ := newTestRuntime(t, client, t0)
	ctx := context.Background()

	// 1回目: Claude pane 0件 → idleSince が t0 にセットされる。
	panesNow = nil
	if shutdown, _, err := r.tick(ctx, cfgPath, t0); err != nil || shutdown {
		t.Fatalf("1回目の tick: shutdown=%v, err=%v, want false, nil", shutdown, err)
	}
	if !r.idleSince.Equal(t0) {
		t.Fatalf("1回目の tick 後 r.idleSince = %v, want %v", r.idleSince, t0)
	}

	// 2回目: 9分後、まだ閾値 (10分) 未満 → shutdown しない、idleSince は据え置き。
	t1 := t0.Add(9 * time.Minute)
	if shutdown, _, err := r.tick(ctx, cfgPath, t1); err != nil || shutdown {
		t.Fatalf("2回目の tick (閾値未満): shutdown=%v, err=%v, want false, nil", shutdown, err)
	}
	if !r.idleSince.Equal(t0) {
		t.Fatalf("2回目の tick 後 r.idleSince = %v, want %v (据え置きのはず)", r.idleSince, t0)
	}

	// 3回目: Claude pane が再出現 → idleSince がリセットされる (ゼロ値に戻る)。
	t2 := t1.Add(time.Second)
	panesNow = []herdrcli.Pane{claudePane("p1", "t1", herdrcli.AgentStatusIdle)}
	if shutdown, _, err := r.tick(ctx, cfgPath, t2); err != nil || shutdown {
		t.Fatalf("3回目の tick (pane再出現): shutdown=%v, err=%v, want false, nil", shutdown, err)
	}
	if !r.idleSince.IsZero() {
		t.Fatalf("pane 再出現後 r.idleSince = %v, want ゼロ値", r.idleSince)
	}

	// 4回目: 再び 0 件 → idleSince が t3 (新しい基準点) にセットされる。
	t3 := t2.Add(time.Second)
	panesNow = nil
	if shutdown, _, err := r.tick(ctx, cfgPath, t3); err != nil || shutdown {
		t.Fatalf("4回目の tick: shutdown=%v, err=%v, want false, nil", shutdown, err)
	}
	if !r.idleSince.Equal(t3) {
		t.Fatalf("4回目の tick 後 r.idleSince = %v, want %v (t0 ではなく t3 が新しい基準点)", r.idleSince, t3)
	}

	// 5回目: t3 から 10分1秒後 → 閾値に達し shutdown。
	t4 := t3.Add(10*time.Minute + time.Second)
	shutdown, cfg, err := r.tick(ctx, cfgPath, t4)
	if err != nil {
		t.Fatalf("5回目の tick() error = %v", err)
	}
	if !shutdown {
		t.Fatal("5回目の tick: shutdown = false, want true (アイドル閾値超過)")
	}
	if cfg.IdleShutdownMinutes != 10 {
		t.Errorf("cfg.IdleShutdownMinutes = %d, want 10 (設定ファイルが反映されていない)", cfg.IdleShutdownMinutes)
	}
}

func TestDaemonRuntimeTick_ConsecutiveFailures(t *testing.T) {
	sentinelErr := errors.New("herdr に到達できない")
	cfgPath := missingConfigPath(t)
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	ctx := context.Background()

	t.Run("閾値まで連続失敗すると自己終了する", func(t *testing.T) {
		client := &herdrclifake.Client{
			PaneListFunc: func(context.Context) ([]herdrcli.Pane, error) { return nil, sentinelErr },
		}
		r, _ := newTestRuntime(t, client, now)

		for i := 1; i < maxConsecutiveTickFailures; i++ {
			shutdown, _, err := r.tick(ctx, cfgPath, now.Add(time.Duration(i)*time.Second))
			if shutdown {
				t.Fatalf("%d 回目の失敗で shutdown してしまった (閾値は %d 回)", i, maxConsecutiveTickFailures)
			}
			if !errors.Is(err, sentinelErr) {
				t.Fatalf("%d 回目の tick() error = %v, want sentinelErr を wrap", i, err)
			}
			if r.consecutiveFailures != i {
				t.Fatalf("%d 回目の tick 後 r.consecutiveFailures = %d, want %d", i, r.consecutiveFailures, i)
			}
		}

		shutdown, _, err := r.tick(ctx, cfgPath, now.Add(time.Duration(maxConsecutiveTickFailures)*time.Second))
		if !shutdown {
			t.Fatalf("%d 回目 (閾値) の tick で shutdown = false, want true", maxConsecutiveTickFailures)
		}
		if !errors.Is(err, sentinelErr) {
			t.Fatalf("%d 回目の tick() error = %v, want sentinelErr を wrap", maxConsecutiveTickFailures, err)
		}
	})

	t.Run("途中で成功するとカウンタがリセットされる", func(t *testing.T) {
		mode := "fail"
		client := &herdrclifake.Client{
			PaneListFunc: func(context.Context) ([]herdrcli.Pane, error) {
				if mode == "fail" {
					return nil, sentinelErr
				}
				return nil, nil
			},
		}
		r, _ := newTestRuntime(t, client, now)

		for i := 1; i <= 3; i++ {
			if _, _, err := r.tick(ctx, cfgPath, now.Add(time.Duration(i)*time.Second)); !errors.Is(err, sentinelErr) {
				t.Fatalf("準備の %d 回目の tick() error = %v, want sentinelErr", i, err)
			}
		}
		if r.consecutiveFailures != 3 {
			t.Fatalf("準備後 r.consecutiveFailures = %d, want 3", r.consecutiveFailures)
		}

		mode = "success"
		successAt := now.Add(4 * time.Second)
		if shutdown, _, err := r.tick(ctx, cfgPath, successAt); err != nil || shutdown {
			t.Fatalf("成功 tick: shutdown=%v, err=%v, want false, nil", shutdown, err)
		}
		if r.consecutiveFailures != 0 {
			t.Fatalf("成功後 r.consecutiveFailures = %d, want 0 (リセットされるはず)", r.consecutiveFailures)
		}

		// リセット後、閾値未満の失敗では shutdown しないことを確認する。
		mode = "fail"
		for i := 1; i < maxConsecutiveTickFailures; i++ {
			shutdown, _, err := r.tick(ctx, cfgPath, successAt.Add(time.Duration(i)*time.Second))
			if shutdown {
				t.Fatalf("リセット後 %d 回目の失敗で shutdown してしまった (カウンタが引き継がれている疑い)", i)
			}
			if !errors.Is(err, sentinelErr) {
				t.Fatalf("リセット後 %d 回目の tick() error = %v, want sentinelErr", i, err)
			}
		}

		// リセット後ちょうど閾値回目の失敗で shutdown する。
		shutdown, _, err := r.tick(ctx, cfgPath, successAt.Add(time.Duration(maxConsecutiveTickFailures)*time.Second))
		if !shutdown {
			t.Fatal("リセット後、閾値回目の失敗で shutdown = false, want true")
		}
		if !errors.Is(err, sentinelErr) {
			t.Fatalf("リセット後、閾値回目の tick() error = %v, want sentinelErr", err)
		}
	})
}

// claudePaneWithSession は agent_session まで埋めた Claude pane を作る。
// pane と Claude セッションの対応付けは AgentSession.Value だけが持つ。
func claudePaneWithSession(paneID, terminalID, sessionID string, status herdrcli.AgentStatus) herdrcli.Pane {
	pane := claudePane(paneID, terminalID, status)
	pane.AgentSession = &herdrcli.AgentSession{
		Agent:  "claude",
		Kind:   "id",
		Source: "herdr:claude",
		Value:  sessionID,
	}
	return pane
}

// writeSessionRateState は 1 セッション分の rate-limits state を dir へ書く。
func writeSessionRateState(t *testing.T, dir, sessionID string, usedPercentage float64, observedAt, resetsAt time.Time) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("state ディレクトリの作成に失敗: %v", err)
	}
	// statusline.sh が書く生の形をそのまま組む (sessionstate が読む側の唯一の実装なので、
	// テストの入力を Go の型から作ると wire format のずれを検出できなくなる)。
	data, err := json.Marshal(map[string]any{
		"five_hour":   map[string]any{"used_percentage": usedPercentage, "resets_at": resetsAt.Unix()},
		"observed_at": observedAt.Unix(),
		"session_id":  sessionID,
	})
	if err != nil {
		t.Fatalf("state の JSON 化に失敗: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, sessionID+".json"), data, 0o644); err != nil {
		t.Fatalf("state の書き込みに失敗: %v", err)
	}
}

// TestDaemonRuntimeTick_RateStateIsMatchedPerSession は、statusline が書く
// 利用上限 state が pane ごとに正しいセッションのものへ対応付けられることを確かめる。
//
// rate-limits state は「そのセッションの」使用率とリセット時刻であって、全 pane に
// 共通の値ではない。1 ファイルを全 pane で共有すると、別セッションの使用率で
// ゲートが働いて上限に達した pane を取りこぼす (2026-08-19 の取りこぼしの原因)。
func TestDaemonRuntimeTick_RateStateIsMatchedPerSession(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	resetsAt := now.Add(time.Hour)

	const limitScreen = "Try again in 5 minutes"

	paneA := claudePaneWithSession("pane-a", "term-a", "session-a", herdrcli.AgentStatusIdle)
	paneB := claudePaneWithSession("pane-b", "term-b", "session-b", herdrcli.AgentStatusIdle)

	client := &herdrclifake.Client{
		PaneListFunc: func(context.Context) ([]herdrcli.Pane, error) {
			return []herdrcli.Pane{paneA, paneB}, nil
		},
		PaneReadFunc: func(context.Context, string, herdrcli.PaneReadOptions) (string, error) {
			return limitScreen, nil
		},
	}

	r, _ := newTestRuntime(t, client, now)
	// session-a は使い切っている。session-b はまだ余裕がある。
	writeSessionRateState(t, r.store.RateLimits, "session-a", 100, now, resetsAt)
	writeSessionRateState(t, r.store.RateLimits, "session-b", 10, now, resetsAt)

	if _, _, err := r.tick(context.Background(), missingConfigPath(t), now); err != nil {
		t.Fatalf("tick() error = %v", err)
	}

	if got := r.panes["term-a"].state.Status; got != monitor.StatusWaiting {
		t.Errorf("pane-a の state.Status = %v, want Waiting "+
			"(session-a は 100%% 消化済みなのでゲートで抑制されてはいけない)", got)
	}
	if got := r.panes["term-b"].state.Status; got != monitor.StatusMonitoring {
		t.Errorf("pane-b の state.Status = %v, want Monitoring "+
			"(session-b は 10%% なのでゲートで抑制されるべき)", got)
	}
}
