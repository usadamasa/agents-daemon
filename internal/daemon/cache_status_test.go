package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/usadamasa/agents-daemon/internal/herdrcli"
	"github.com/usadamasa/agents-daemon/internal/herdrcli/herdrclifake"
	"github.com/usadamasa/agents-daemon/internal/monitor"
	"github.com/usadamasa/agents-daemon/internal/sessionstate"
)

const cacheLastRequestRaw = "2026-09-27T03:44:05.954Z"

// cacheTranscript は最後の応答が cacheLastRequestRaw に始まり､1h の cache write を持つ transcript｡
const cacheTranscript = `{"type":"user","timestamp":"2026-09-27T03:44:00.000Z"}
{"type":"assistant","isSidechain":false,"timestamp":"` + cacheLastRequestRaw + `","message":{"id":"msg_1","model":"claude-fable-5-1","usage":{"input_tokens":3,"cache_creation_input_tokens":500,"cache_read_input_tokens":1000,"cache_creation":{"ephemeral_1h_input_tokens":500,"ephemeral_5m_input_tokens":0}}}}
`

// writeCacheTranscript は transcript を置き､statusline が書く形の context/<sid>.json からそこを指す｡
// context の使用率は 63%｡
func writeCacheTranscript(t *testing.T, store sessionstate.Store, sessionID string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), sessionID+".jsonl")
	if err := os.WriteFile(path, []byte(cacheTranscript), 0o644); err != nil {
		t.Fatalf("transcript の書き込みに失敗: %v", err)
	}
	writeContextSidecar(t, store.Context, sessionID, map[string]any{
		"used_percentage": 63, "observed_at": 1786840000, "transcript_path": path,
	})
}

// writeContextSidecar は ingest-statusline が書く生の形で context/<sid>.json を置く｡
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

func TestDaemonRuntimeTick_StatusCarriesCacheState(t *testing.T) {
	last := time.Date(2026, 9, 27, 3, 44, 5, 954_000_000, time.UTC)
	pane := claudePaneWithSession("pane-a", "term-a", "session-a", herdrcli.AgentStatusIdle)
	client := &herdrclifake.Client{
		PaneListFunc: func(context.Context) ([]herdrcli.Pane, error) { return []herdrcli.Pane{pane}, nil },
		PaneReadFunc: func(context.Context, string, herdrcli.PaneReadOptions) (string, error) {
			return "$ some normal claude output\n⏺ done", nil
		},
	}

	t.Run("warm な cache は失効時刻とともに出る", func(t *testing.T) {
		now := last.Add(10 * time.Minute)
		r, statusPath := newTestRuntime(t, client, now)
		writeCacheTranscript(t, r.store, "session-a")

		if _, _, err := r.tick(context.Background(), missingConfigPath(t), now); err != nil {
			t.Fatalf("tick() error = %v", err)
		}
		snap, err := ReadStatusSnapshot(statusPath)
		if err != nil || snap == nil || len(snap.Panes) != 1 {
			t.Fatalf("ReadStatusSnapshot() = %+v, %v", snap, err)
		}
		c := snap.Panes[0].Cache
		if c == nil {
			t.Fatal("Panes[0].Cache = nil, want sidecar の状態")
		}
		if c.State != "warm" || c.TTLSeconds != 3600 || !c.LastRequestAt.Equal(last) || !c.ExpiresAt.Equal(last.Add(time.Hour)) {
			t.Errorf("Cache = %+v, want warm / 3600 / %v / %v", c, last, last.Add(time.Hour))
		}
	})

	t.Run("失効していれば expired", func(t *testing.T) {
		now := last.Add(5 * time.Hour)
		r, statusPath := newTestRuntime(t, client, now)
		writeCacheTranscript(t, r.store, "session-a")

		if _, _, err := r.tick(context.Background(), missingConfigPath(t), now); err != nil {
			t.Fatalf("tick() error = %v", err)
		}
		snap, err := ReadStatusSnapshot(statusPath)
		if err != nil || snap == nil || len(snap.Panes) != 1 {
			t.Fatalf("ReadStatusSnapshot() = %+v, %v", snap, err)
		}
		if c := snap.Panes[0].Cache; c == nil || c.State != "expired" {
			t.Errorf("Cache = %+v, want expired", c)
		}
	})

	t.Run("working な pane では transcript を読まない", func(t *testing.T) {
		// 応答のたびに伸びる transcript を tick ごとに読み直さない｡読んだことが無ければ省略する｡
		working := claudePaneWithSession("pane-a", "term-a", "session-a", herdrcli.AgentStatusWorking)
		workingClient := &herdrclifake.Client{
			PaneListFunc: func(context.Context) ([]herdrcli.Pane, error) { return []herdrcli.Pane{working}, nil },
			PaneReadFunc: client.PaneReadFunc,
		}
		now := last.Add(10 * time.Minute)
		r, statusPath := newTestRuntime(t, workingClient, now)
		writeCacheTranscript(t, r.store, "session-a")

		if _, _, err := r.tick(context.Background(), missingConfigPath(t), now); err != nil {
			t.Fatalf("tick() error = %v", err)
		}
		snap, err := ReadStatusSnapshot(statusPath)
		if err != nil || snap == nil || len(snap.Panes) != 1 {
			t.Fatalf("ReadStatusSnapshot() = %+v, %v", snap, err)
		}
		if c := snap.Panes[0].Cache; c != nil {
			t.Errorf("Cache = %+v, want nil (読んでいない)", c)
		}
	})

	t.Run("cache が無ければ省略する", func(t *testing.T) {
		now := last.Add(10 * time.Minute)
		r, statusPath := newTestRuntime(t, client, now)

		if _, _, err := r.tick(context.Background(), missingConfigPath(t), now); err != nil {
			t.Fatalf("tick() error = %v", err)
		}
		snap, err := ReadStatusSnapshot(statusPath)
		if err != nil || snap == nil || len(snap.Panes) != 1 {
			t.Fatalf("ReadStatusSnapshot() = %+v, %v", snap, err)
		}
		if snap.Panes[0].Cache != nil {
			t.Errorf("Cache = %+v, want nil", snap.Panes[0].Cache)
		}
		data, _ := os.ReadFile(statusPath)
		if strings.Contains(string(data), `"cache"`) {
			t.Errorf("status.json に cache キーが出ている:\n%s", data)
		}
	})
}

// waitingPaneClient は待機明けで limit 画面のままの pane を 1 枚返す client｡
func waitingPaneClient(pane herdrcli.Pane) *herdrclifake.Client {
	return &herdrclifake.Client{
		PaneListFunc: func(context.Context) ([]herdrcli.Pane, error) { return []herdrcli.Pane{pane}, nil },
		PaneReadFunc: func(context.Context, string, herdrcli.PaneReadOptions) (string, error) {
			return "Try again in 5 minutes", nil
		},
	}
}

func TestDaemonRuntimeTick_SendLogCarriesCacheState(t *testing.T) {
	last := time.Date(2026, 9, 27, 3, 44, 5, 954_000_000, time.UTC)
	pane := claudePaneWithSession("pane-a", "term-a", "session-a", herdrcli.AgentStatusIdle)

	t.Run("失効後の送信は ack を書き､経過と TTL と context 使用率をログに残す", func(t *testing.T) {
		now := last.Add(5*time.Hour + 12*time.Minute)
		client := waitingPaneClient(pane)
		r, statusPath := newTestRuntime(t, client, now)
		writeCacheTranscript(t, r.store, "session-a")
		r.panes["term-a"] = &paneEntry{state: &monitor.PaneState{Status: monitor.StatusWaiting}}

		if _, _, err := r.tick(context.Background(), missingConfigPath(t), now); err != nil {
			t.Fatalf("tick() error = %v", err)
		}

		ack, err := os.ReadFile(filepath.Join(r.store.CacheAck, "session-a"))
		if err != nil {
			t.Fatalf("ack が書かれていない: %v", err)
		}
		if string(ack) != cacheLastRequestRaw {
			t.Errorf("ack = %q, want %q", ack, cacheLastRequestRaw)
		}
		log := readLog(t, statusPath)
		for _, want := range []string{"再開メッセージを送信しました", "cache=expired", "経過 312 分", "TTL 60 分", "context 使用率 63%"} {
			if !strings.Contains(log, want) {
				t.Errorf("ログに %q が無い:\n%s", want, log)
			}
		}
	})

	t.Run("cache が無ければ sentinel を書き､unknown とログに残す", func(t *testing.T) {
		now := last.Add(5 * time.Hour)
		client := waitingPaneClient(pane)
		r, statusPath := newTestRuntime(t, client, now)
		r.panes["term-a"] = &paneEntry{state: &monitor.PaneState{Status: monitor.StatusWaiting}}

		if _, _, err := r.tick(context.Background(), missingConfigPath(t), now); err != nil {
			t.Fatalf("tick() error = %v", err)
		}
		ack, err := os.ReadFile(filepath.Join(r.store.CacheAck, "session-a"))
		if err != nil {
			t.Fatalf("ack が書かれていない: %v", err)
		}
		if string(ack) != "daemon-unknown" {
			t.Errorf("ack = %q, want daemon-unknown", ack)
		}
		if log := readLog(t, statusPath); !strings.Contains(log, "cache=unknown") {
			t.Errorf("ログに cache=unknown が無い:\n%s", log)
		}
	})

	t.Run("dry-run では ack を書かない", func(t *testing.T) {
		// 書くと利用者自身の次の prompt が guard を素通りする｡
		now := last.Add(5 * time.Hour)
		client := waitingPaneClient(pane)
		r, statusPath := newTestRuntime(t, client, now)
		r.dryRun = true
		writeCacheTranscript(t, r.store, "session-a")
		r.panes["term-a"] = &paneEntry{state: &monitor.PaneState{Status: monitor.StatusWaiting}}

		if _, _, err := r.tick(context.Background(), missingConfigPath(t), now); err != nil {
			t.Fatalf("tick() error = %v", err)
		}
		if _, err := os.Stat(filepath.Join(r.store.CacheAck, "session-a")); !os.IsNotExist(err) {
			t.Errorf("dry-run なのに ack が書かれている (err = %v)", err)
		}
		log := readLog(t, statusPath)
		if !strings.Contains(log, "cache=expired") || !strings.Contains(log, "ack は dry-run で省略") {
			t.Errorf("dry-run でも cache の状態はログに残し､ack を書いていないことを明記する:\n%s", log)
		}
		if strings.Contains(log, "ack 済み") {
			t.Errorf("書いていない ack を「済み」と報告している:\n%s", log)
		}
	})
}
