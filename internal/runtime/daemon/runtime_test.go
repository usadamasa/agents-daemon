package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/usadamasa/agents-daemon/internal/applog"
	"github.com/usadamasa/agents-daemon/internal/daemonstate"
	"github.com/usadamasa/agents-daemon/internal/multiplexer"
	"github.com/usadamasa/agents-daemon/internal/multiplexer/multiplexerfake"
	"github.com/usadamasa/agents-daemon/internal/runtime/daemon/limitlog"
	"github.com/usadamasa/agents-daemon/internal/runtime/monitor"
	"github.com/usadamasa/agents-daemon/internal/sessionstate"
	"github.com/usadamasa/agents-daemon/internal/turnfailure"
)

// newTestRuntime は daemonRuntime を t.TempDir() の下だけで完結させて組み立てる。
// statusPath はログディレクトリと同じ一時ディレクトリ内に置く。
func newTestRuntime(t *testing.T, client multiplexerClient, startedAt time.Time) (*daemonRuntime, string) {
	t.Helper()
	dir := t.TempDir()
	log := applog.NewLogger(filepath.Join(dir, "daemon.log"), time.Now)
	t.Cleanup(func() { _ = log.Close() })
	statusPath := filepath.Join(dir, "status.json")
	// sidecar file のディレクトリは一時ディレクトリ配下を指すだけで作らない。
	// 存在しないディレクトリは「state 無し」として扱われる。
	// 利用上限 state が要るテストは r.store.RateLimits.Dir へ書く。
	r := newDaemonRuntime(client, log, statusPath, sessionstate.New(dir), func(time.Duration) {}, startedAt)
	r.limits = limitlog.New(dir)
	r.turnFailures = turnfailure.New(dir)
	// Claude Code の設定ディレクトリ (登録簿と transcript) も一時ディレクトリの下に置く。
	r.attachClaudeConfigDir(filepath.Join(dir, "claude"))
	return r, statusPath
}

// transcript の末尾に置く entry (transcript/testdata と同じ形)。
const (
	// transcriptLimited は 5 時間ウィンドウの上限で止まった turn。resetsAt は 2026-10-04T12:30:00Z。
	transcriptLimited = `{"type":"assistant","uuid":"cae11b0e-b253-403c-8cd6-7cf350809a97","timestamp":"2026-10-04T08:32:13.884Z","message":{"role":"assistant","content":[{"type":"text","text":"You've hit your session limit · resets 9:30pm (Asia/Tokyo)"}]},"quotaLimits":{"status":"rejected","resetsAt":1791117000,"rateLimitType":"five_hour","overageStatus":"rejected"},"error":"rate_limit","isApiErrorMessage":true,"apiErrorStatus":429,"version":"2.1.289"}`
	// transcriptIdle は普通に終わった turn。
	transcriptIdle = `{"type":"assistant","uuid":"7a1f0c2e-5d3b-4e8f-9a6c-1b2d3e4f5a6b","timestamp":"2026-10-04T08:20:53.022Z","message":{"role":"assistant","content":[{"type":"text","text":"done"}]},"version":"2.1.289"}`
)

// transcriptResetsAt は transcriptLimited の resetsAt。
var transcriptResetsAt = time.Unix(1791117000, 0)

// writeTranscript は sessionID の transcript を Claude Code の置き場に置く。
func writeTranscript(t *testing.T, r *daemonRuntime, sessionID string, lines ...string) {
	t.Helper()
	dir := filepath.Join(r.transcripts.Dir, "-work-project")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, sessionID+".jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// writeSessionRegistry は Claude Code の登録簿 (sessions/<pid>.json) を置く。
func writeSessionRegistry(t *testing.T, r *daemonRuntime, pid int, sessionID string) {
	t.Helper()
	if err := os.MkdirAll(r.claudeSessions.Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(`{"pid":%d,"sessionId":%q,"name":"peer"}`, pid, sessionID)
	if err := os.WriteFile(filepath.Join(r.claudeSessions.Dir, fmt.Sprintf("%d.json", pid)), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// writeContextSidecar は statusline が書く生の形で context/<sid>.json を置く。
func writeContextSidecar(t *testing.T, dir, sessionID string, fields map[string]any) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("context ディレクトリの作成に失敗: %v", err)
	}
	fields["session_id"] = sessionID
	raw, err := json.Marshal(fields)
	if err != nil {
		t.Fatalf("context の JSON 化に失敗: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, sessionID+".json"), raw, 0o644); err != nil {
		t.Fatalf("context sidecar の書き込みに失敗: %v", err)
	}
}

func readLog(t *testing.T, statusPath string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(filepath.Dir(statusPath), "daemon.log"))
	if err != nil {
		t.Fatalf("ログの読み込みに失敗: %v", err)
	}
	return string(data)
}

func claudePane(paneID string, status multiplexer.AgentStatus) multiplexer.Pane {
	return multiplexer.Pane{
		Agent:       "claude-code",
		AgentStatus: status,
		PaneID:      paneID,
		CWD:         "/tmp/project",
	}
}

func nonClaudePane(paneID string) multiplexer.Pane {
	return multiplexer.Pane{
		Agent:       "bash",
		AgentStatus: multiplexer.AgentStatusIdle,
		PaneID:      paneID,
	}
}

// missingConfigPath は「まだファイルが無い」正常系を意図的に使う場合の共通パス。
// config.Load は os.ErrNotExist をエラー扱いしない。
func missingConfigPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "config.json")
}

func TestDaemonRuntimeTick_NewPaneGetsFreshState(t *testing.T) {
	client := &multiplexerfake.Client{
		PaneListFunc: func(context.Context) ([]multiplexer.Pane, error) {
			return []multiplexer.Pane{claudePane("p1", multiplexer.AgentStatusIdle)}, nil
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
	entry, ok := r.panes["p1"]
	if !ok {
		t.Fatal("pane p1 のエントリが作られていない")
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
}

func TestDaemonRuntimeTick_DisappearedPaneEntryDropped(t *testing.T) {
	var panesNow []multiplexer.Pane
	client := &multiplexerfake.Client{
		PaneListFunc: func(context.Context) ([]multiplexer.Pane, error) {
			return panesNow, nil
		},
	}
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	r, _ := newTestRuntime(t, client, now)
	cfgPath := missingConfigPath(t)

	panesNow = []multiplexer.Pane{claudePane("p1", multiplexer.AgentStatusIdle)}
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
	client := &multiplexerfake.Client{
		PaneListFunc: func(context.Context) ([]multiplexer.Pane, error) {
			return []multiplexer.Pane{nonClaudePane("p1")}, nil
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

// 上限かどうかは pane ごとに、そのセッションの transcript の末尾で決める。片方の transcript が上限でも、もう片方は巻き込まれない。
func TestDaemonRuntimeTick_LimitIsDecidedPerSessionTranscript(t *testing.T) {
	now := time.Date(2026, 10, 4, 9, 0, 0, 0, time.UTC)
	paneA := claudePaneWithSession("pane-a", "session-a", multiplexer.AgentStatusError)
	paneB := claudePaneWithSession("pane-b", "session-b", multiplexer.AgentStatusIdle)
	client := &multiplexerfake.Client{
		PaneListFunc: func(context.Context) ([]multiplexer.Pane, error) { return []multiplexer.Pane{paneA, paneB}, nil },
	}
	r, _ := newTestRuntime(t, client, now)
	writeTranscript(t, r, "session-a", transcriptIdle, transcriptLimited)
	writeTranscript(t, r, "session-b", transcriptLimited, transcriptIdle)

	if _, _, err := r.tick(context.Background(), missingConfigPath(t), now); err != nil {
		t.Fatalf("tick() error = %v", err)
	}

	a := r.panes["pane-a"].state
	if a.Status != monitor.StatusWaiting || !a.WaitUntil.Equal(transcriptResetsAt.Add(60*time.Second)) {
		t.Errorf("pane-a の state = %+v, want transcript の resetsAt + margin まで待機", a)
	}
	if got := r.panes["pane-b"].state.Status; got != monitor.StatusMonitoring {
		t.Errorf("pane-b の state.Status = %v, want Monitoring (最後の turn は普通に終わっている)", got)
	}
	// 検知の tick では画面を読まない。
	for _, call := range client.Calls {
		if strings.HasPrefix(call, "PaneRead") {
			t.Errorf("検知の tick で画面を読んだ: %v", client.Calls)
		}
	}
}

// statusline の記録から session_id の引けない pane は、前景の pid で Claude Code の登録簿から補う。
// 補えなければ検知の対象外で、理由を status.json に書く。
func TestDaemonRuntimeTick_SessionIDFromRegistryPID(t *testing.T) {
	now := time.Date(2026, 10, 4, 9, 0, 0, 0, time.UTC)
	known := claudePane("pane-a", multiplexer.AgentStatusError)
	known.PID = 4242
	unknown := claudePane("pane-b", multiplexer.AgentStatusIdle)
	unknown.PID = 9999
	client := &multiplexerfake.Client{
		PaneListFunc: func(context.Context) ([]multiplexer.Pane, error) { return []multiplexer.Pane{known, unknown}, nil },
	}
	r, statusPath := newTestRuntime(t, client, now)
	writeSessionRegistry(t, r, 4242, "session-a")
	writeTranscript(t, r, "session-a", transcriptLimited)

	if _, _, err := r.tick(context.Background(), missingConfigPath(t), now); err != nil {
		t.Fatalf("tick() error = %v", err)
	}

	if got := r.panes["pane-a"].state.Status; got != monitor.StatusWaiting {
		t.Errorf("pane-a の state.Status = %v, want Waiting (登録簿の pid で session を引いて transcript を読む)", got)
	}
	snap, err := daemonstate.ReadStatusSnapshot(statusPath)
	if err != nil || snap == nil || len(snap.Panes) != 2 {
		t.Fatalf("ReadStatusSnapshot() = %+v, %v", snap, err)
	}
	byPane := map[string]daemonstate.PaneStatus{}
	for _, p := range snap.Panes {
		byPane[p.PaneID] = p
	}
	if got := byPane["pane-a"]; got.SessionID != "session-a" || got.DetectReason != "" {
		t.Errorf("pane-a の status = %+v, want session_id が補われ理由無し", got)
	}
	if got := byPane["pane-b"]; got.SessionID != "" || got.LastOutcome != "no-session" || !strings.Contains(got.DetectReason, "session_id") {
		t.Errorf("pane-b の status = %+v, want no-session と理由", got)
	}
	log := readLog(t, statusPath)
	if !strings.Contains(log, "pane pane-b") || !strings.Contains(log, "session_id を引けない") {
		t.Errorf("判定できない理由がログに無い:\n%s", log)
	}
	// 同じ理由が続く間は書き直さない。
	if _, _, err := r.tick(context.Background(), missingConfigPath(t), now.Add(5*time.Second)); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(readLog(t, statusPath), "session_id を引けない"); n != 1 {
		t.Errorf("理由のログが %d 回 (want 1)", n)
	}
}

// StopFailure の記録が rate_limit なのに transcript が上限と読めなければ、transcript の形が変わった疑いとしてログに残す。
func TestDaemonRuntimeTick_LogsMismatchWithTurnFailure(t *testing.T) {
	now := time.Date(2026, 10, 4, 9, 0, 0, 0, time.UTC)
	pane := claudePaneWithSession("pane-a", "session-a", multiplexer.AgentStatusError)
	client := &multiplexerfake.Client{
		PaneListFunc: func(context.Context) ([]multiplexer.Pane, error) { return []multiplexer.Pane{pane}, nil },
	}
	r, statusPath := newTestRuntime(t, client, now)
	writeTranscript(t, r, "session-a", transcriptIdle)
	input := `{"session_id": "session-a", "hook_event_name": "StopFailure", "error": "rate_limit"}`
	if err := r.turnFailures.Ingest([]byte(input), now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}

	for i := range 2 {
		if _, _, err := r.tick(context.Background(), missingConfigPath(t), now.Add(time.Duration(i)*5*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	log := readLog(t, statusPath)
	if n := strings.Count(log, "transcript の形が変わった疑い"); n != 1 {
		t.Errorf("食い違いのログが %d 回 (want 1):\n%s", n, log)
	}
	if got := r.panes["pane-a"].state.Status; got != monitor.StatusMonitoring {
		t.Errorf("state.Status = %v, want Monitoring (hook の記録だけでは待機に入らない)", got)
	}
}

// record の kind / msg は status.json と、待機に入る行のログに出す。
func TestDaemonRuntimeTick_RecordInStatusAndLog(t *testing.T) {
	now := time.Date(2026, 10, 4, 9, 0, 0, 0, time.UTC)
	pane := claudePaneWithSession("p1", "session-a", multiplexer.AgentStatusBlocked)
	pane.AgentKind = "permission"
	pane.AgentMessage = "approve Bash: rm -rf ./tmp/junk"
	client := &multiplexerfake.Client{
		PaneListFunc: func(context.Context) ([]multiplexer.Pane, error) { return []multiplexer.Pane{pane}, nil },
	}
	r, statusPath := newTestRuntime(t, client, now)
	writeTranscript(t, r, "session-a", transcriptLimited)

	if _, _, err := r.tick(context.Background(), missingConfigPath(t), now); err != nil {
		t.Fatalf("tick() error = %v", err)
	}

	snap, err := daemonstate.ReadStatusSnapshot(statusPath)
	if err != nil || snap == nil || len(snap.Panes) != 1 {
		t.Fatalf("ReadStatusSnapshot() = %+v, %v; want pane 1 件", snap, err)
	}
	if got := snap.Panes[0]; got.AgentKind != "permission" || got.AgentMessage != pane.AgentMessage {
		t.Errorf("status.json の record = kind %q, message %q; want permission, %q", got.AgentKind, got.AgentMessage, pane.AgentMessage)
	}
	log := readLog(t, statusPath)
	if !strings.Contains(log, "待機に入りました") || !strings.Contains(log, "kind=permission") || !strings.Contains(log, pane.AgentMessage) {
		t.Errorf("待機のログに record が無い:\n%s", log)
	}
}

func TestDaemonRuntimeTick_IdleShutdown(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(cfgPath, []byte(`{"idleShutdownMinutes": 10}`), 0o644); err != nil {
		t.Fatalf("設定ファイルの書き込みに失敗: %v", err)
	}

	var panesNow []multiplexer.Pane
	client := &multiplexerfake.Client{
		PaneListFunc: func(context.Context) ([]multiplexer.Pane, error) { return panesNow, nil },
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
	panesNow = []multiplexer.Pane{claudePane("p1", multiplexer.AgentStatusIdle)}
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
	sentinelErr := errors.New("rex に到達できない")
	cfgPath := missingConfigPath(t)
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	ctx := context.Background()

	t.Run("閾値まで連続失敗すると自己終了する", func(t *testing.T) {
		client := &multiplexerfake.Client{
			PaneListFunc: func(context.Context) ([]multiplexer.Pane, error) { return nil, sentinelErr },
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
		client := &multiplexerfake.Client{
			PaneListFunc: func(context.Context) ([]multiplexer.Pane, error) {
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

// claudePaneWithSession は SessionID (backend が sidecar から引いた値) まで埋めた Claude pane を作る。
// pane と Claude セッションの対応付けは SessionID だけが持つ。
func claudePaneWithSession(paneID, sessionID string, status multiplexer.AgentStatus) multiplexer.Pane {
	pane := claudePane(paneID, status)
	pane.SessionID = sessionID
	return pane
}
