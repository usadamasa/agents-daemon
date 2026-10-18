package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/usadamasa/agents-daemon/internal/multiplexer"
	"github.com/usadamasa/agents-daemon/internal/multiplexer/multiplexerfake"
)

// 旧版の PostCompact hook が置いた compacted/<sid> が残っていても、daemon は再開を送らない。
// Claude Code 本体の idle compaction の後に離席中の pane へ「Continue the task」を打ち込んだ
// 2 件 (2026-10-13、10-16) がこのテストの元。compact 後の再開は daemon の役目ではない。
func TestDaemonRuntimeTick_StaleCompactedMarkerSendsNothing(t *testing.T) {
	now := time.Date(2026, 10, 16, 3, 0, 22, 0, time.UTC)
	pane := claudePaneWithSession("pane-a", "session-a", multiplexer.AgentStatusIdle)
	client := &multiplexerfake.Client{
		PaneListFunc: func(context.Context) ([]multiplexer.Pane, error) { return []multiplexer.Pane{pane}, nil },
		PaneReadFunc: func(context.Context, string, multiplexer.PaneReadOptions) (string, error) {
			return "⏺ Compacted while idle, before the prompt cache expired\n\n─────────\n❯\n─────────\n", nil
		},
	}
	r, statusPath := newTestRuntime(t, client, now)
	writeContextSidecar(t, r.store.Context.Dir, "session-a", map[string]any{
		"used_percentage": 25, "observed_at": now.Add(-time.Hour).Unix(),
	})
	compactedDir := filepath.Join(filepath.Dir(r.store.Context.Dir), "compacted")
	marker := filepath.Join(compactedDir, "session-a")
	if err := os.MkdirAll(compactedDir, 0o755); err != nil {
		t.Fatalf("compacted ディレクトリの作成に失敗: %v", err)
	}
	if err := os.WriteFile(marker, nil, 0o644); err != nil {
		t.Fatalf("marker の書き込みに失敗: %v", err)
	}
	// 3 段目の猶予 (旧 compactResumeDelaySeconds = 60) を過ぎた marker。
	if err := os.Chtimes(marker, now.Add(-2*time.Minute), now.Add(-2*time.Minute)); err != nil {
		t.Fatalf("marker の mtime 変更に失敗: %v", err)
	}

	if _, _, err := r.tick(context.Background(), missingConfigPath(t), now); err != nil {
		t.Fatalf("tick() error = %v", err)
	}

	for _, call := range client.Calls {
		if strings.HasPrefix(call, "SendText") || strings.HasPrefix(call, "SendKeys") {
			t.Fatalf("compact 後の pane へ送信した: %v", client.Calls)
		}
	}
	if got := r.panes["pane-a"].lastLogKey; got != "monitoring" {
		t.Errorf("outcome = %q, want monitoring", got)
	}
	// 静かな tick はログファイルも作らない。
	if _, err := os.Stat(filepath.Join(filepath.Dir(statusPath), "daemon.log")); !os.IsNotExist(err) {
		t.Errorf("何も送っていないのにログが書かれた (err=%v):\n%s", err, readLog(t, statusPath))
	}
}
