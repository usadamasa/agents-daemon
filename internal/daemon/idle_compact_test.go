package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/usadamasa/agents-daemon/internal/herdrcli"
	"github.com/usadamasa/agents-daemon/internal/herdrcli/herdrclifake"
	"github.com/usadamasa/agents-daemon/internal/monitor"
)

// idleCompactConfigPath は idle compact を有効にした config.json を置く｡
func idleCompactConfigPath(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"cacheIdleCompactEnabled": true}`), 0o600); err != nil {
		t.Fatalf("config.json の書き込みに失敗: %v", err)
	}
	return path
}

// probeTranscript は実機の probe と同じ形の transcript｡tool_use の応答 (使用率 28%) の後に
// end_turn の応答 (69%) が 2 行と stop_hook_summary が続く｡Stop hook の時点では end_turn の行が
// まだ書かれておらず､tool_use の応答しか読めなかった｡
const probeTranscript = `{"type":"assistant","isSidechain":false,"timestamp":"2026-09-29T03:36:48.392Z","message":{"id":"msg_tool","model":"claude-haiku-4-5-20251001","stop_reason":"tool_use","usage":{"input_tokens":8,"cache_creation_input_tokens":1739,"cache_read_input_tokens":54617,"output_tokens":2030,"cache_creation":{"ephemeral_1h_input_tokens":1739,"ephemeral_5m_input_tokens":0}}}}
{"type":"user","timestamp":"2026-09-29T03:37:01.182Z"}
{"type":"assistant","isSidechain":false,"timestamp":"2026-09-29T03:37:06.094Z","message":{"id":"msg_end","model":"claude-haiku-4-5-20251001","stop_reason":"end_turn","usage":{"input_tokens":8,"cache_creation_input_tokens":80670,"cache_read_input_tokens":56356,"output_tokens":329,"cache_creation":{"ephemeral_1h_input_tokens":80670,"ephemeral_5m_input_tokens":0}}}}
{"type":"assistant","isSidechain":false,"timestamp":"2026-09-29T03:37:06.163Z","message":{"id":"msg_end","model":"claude-haiku-4-5-20251001","stop_reason":"end_turn","usage":{"input_tokens":8,"cache_creation_input_tokens":80670,"cache_read_input_tokens":56356,"output_tokens":329,"cache_creation":{"ephemeral_1h_input_tokens":80670,"ephemeral_5m_input_tokens":0}}}}
{"type":"system","subtype":"stop_hook_summary","timestamp":"2026-09-29T03:37:06.370Z"}
`

func TestDaemonRuntimeTick_IdleCompactFromTranscript(t *testing.T) {
	// idle な pane では statusline が再描画されず､観測は最後の応答の直後で止まる｡
	lastResponse := time.Date(2026, 9, 29, 3, 37, 6, 94_000_000, time.UTC)
	now := lastResponse.Add(51 * time.Minute)
	pane := claudePaneWithSession("pane-a", "term-a", "session-a", herdrcli.AgentStatusIdle)
	client := &herdrclifake.Client{
		PaneListFunc: func(context.Context) ([]herdrcli.Pane, error) { return []herdrcli.Pane{pane}, nil },
		PaneReadFunc: func(context.Context, string, herdrcli.PaneReadOptions) (string, error) {
			return "⏺ 読み終えました｡\n\n─────────\n❯\n─────────\n", nil
		},
	}
	r, statusPath := newTestRuntime(t, client, now)
	transcript := filepath.Join(t.TempDir(), "session-a.jsonl")
	if err := os.WriteFile(transcript, []byte(probeTranscript), 0o644); err != nil {
		t.Fatalf("transcript の書き込みに失敗: %v", err)
	}
	writeContextSidecar(t, r.store.Context, "session-a", map[string]any{
		"used_percentage": 69, "observed_at": lastResponse.Add(time.Second).Unix(),
		"context_window_size": 200000, "transcript_path": transcript,
	})

	if _, _, err := r.tick(context.Background(), idleCompactConfigPath(t), now); err != nil {
		t.Fatalf("tick() error = %v", err)
	}

	log := readLog(t, statusPath)
	for _, want := range []string{"idle compact: cache の失効が近いため compact-prep を投入しました", "cache=warm (経過 51 分"} {
		if !strings.Contains(log, want) {
			t.Errorf("ログに %q が無い:\n%s", want, log)
		}
	}
}

func TestDaemonRuntimeTick_IdleCompactMarker(t *testing.T) {
	last := time.Date(2026, 9, 27, 3, 44, 5, 954_000_000, time.UTC)
	now := last.Add(50 * time.Minute)
	pane := claudePaneWithSession("pane-a", "term-a", "session-a", herdrcli.AgentStatusIdle)
	client := &herdrclifake.Client{
		PaneListFunc: func(context.Context) ([]herdrcli.Pane, error) { return []herdrcli.Pane{pane}, nil },
		PaneReadFunc: func(context.Context, string, herdrcli.PaneReadOptions) (string, error) {
			return "⏺ 変更を適用しました｡\n\n─────────\n❯\n─────────\n", nil
		},
	}

	// prepPending は idle 起点で compact-prep を送り､state file が書かれた後の状態を作る｡
	prepPending := func(t *testing.T, r *daemonRuntime) {
		t.Helper()
		writeCacheTranscript(t, r.store, "session-a")
		prep := filepath.Join(r.store.CompactState, "session-a.md")
		if err := os.MkdirAll(r.store.CompactState, 0o755); err != nil {
			t.Fatalf("compact-state ディレクトリの作成に失敗: %v", err)
		}
		if err := os.WriteFile(prep, []byte("# state\n"), 0o644); err != nil {
			t.Fatalf("state file の書き込みに失敗: %v", err)
		}
		if err := os.Chtimes(prep, now.Add(-10*time.Second), now.Add(-10*time.Second)); err != nil {
			t.Fatalf("state file の mtime 変更に失敗: %v", err)
		}
		r.panes["term-a"] = &paneEntry{state: &monitor.PaneState{
			CompactPrepSentAt: now.Add(-40 * time.Second),
			CompactAutoSentAt: now.Add(-40 * time.Second),
			CompactPrepIdle:   true,
		}}
	}

	t.Run("/compact の前に marker を書き､ログに残す", func(t *testing.T) {
		r, statusPath := newTestRuntime(t, client, now)
		prepPending(t, r)

		if _, _, err := r.tick(context.Background(), idleCompactConfigPath(t), now); err != nil {
			t.Fatalf("tick() error = %v", err)
		}
		if _, err := os.Stat(filepath.Join(r.store.IdleCompacted, "session-a")); err != nil {
			t.Errorf("idle compact の marker が書かれていない: %v", err)
		}
		if log := readLog(t, statusPath); !strings.Contains(log, "idle compact") || !strings.Contains(log, "cache=warm") {
			t.Errorf("ログに idle compact の送信と cache の状態が無い:\n%s", log)
		}
	})

	t.Run("dry-run では marker を書かない", func(t *testing.T) {
		r, _ := newTestRuntime(t, client, now)
		r.dryRun = true
		prepPending(t, r)

		if _, _, err := r.tick(context.Background(), idleCompactConfigPath(t), now); err != nil {
			t.Fatalf("tick() error = %v", err)
		}
		if _, err := os.Stat(filepath.Join(r.store.IdleCompacted, "session-a")); !os.IsNotExist(err) {
			t.Errorf("dry-run なのに marker が書かれている (err = %v)", err)
		}
	})
}
