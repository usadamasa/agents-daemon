package rexcli

import (
	"context"
	"strings"
	"testing"
)

// runJSON は `run --json` の応答。形は 2026-10-21 の実測 (preview.17)。
const runJSON = `{"window_id":"window:01a15e6a-7ff4-74de-9808-aab36ef00d8b","block_ids":["block:01a15e6a-7fe3-7298-af51-8982a808f03b"],"revision":12}`

// TestExecClient_RunWindow は新しい window でコマンドを起こす配線を見る。フォーカスを奪わず、終了後も block を残し、
// 開いたセッションの rex session に置き、argv はそのまま渡す (空白を含む引数も 1 つのまま。改行を含む引数が 1 つのまま
// 子へ届くことは fake の calls.log が行で割れるのでここでは見ず、実機で確かめた)。
func TestExecClient_RunWindow(t *testing.T) {
	f := newFakeRex(t)
	f.put(t, "run.json", runJSON)
	c := f.client(staticResolver(nil))

	prompt := "パーキングロットのメモ「x」をタスクにしてください。 詳細: a b"
	got, err := c.RunWindow(context.Background(), sessionA, "/w/claude-config", "claude-config", []string{"claude", "--session-id", sidDone, "--", prompt})
	if err != nil {
		t.Fatalf("RunWindow() error = %v", err)
	}
	if want := "block:01a15e6a-7fe3-7298-af51-8982a808f03b"; got != want {
		t.Errorf("RunWindow() = %q, want %q", got, want)
	}
	f.assertCalls(t,
		"--autostart=false run --focus=false --keep-open -s "+sessionA+" --cwd /w/claude-config --label claude-config --json -- claude --session-id "+sidDone+" -- "+shellQuote(prompt),
	)
}

// session と label が無ければ付けない (rex の既定の session に置く)。
func TestExecClient_RunWindow_Minimal(t *testing.T) {
	f := newFakeRex(t)
	f.put(t, "run.json", runJSON)
	c := f.client(staticResolver(nil))
	if _, err := c.RunWindow(context.Background(), "", "/w/x", "", []string{"claude"}); err != nil {
		t.Fatalf("RunWindow() error = %v", err)
	}
	f.assertCalls(t, "--autostart=false run --focus=false --keep-open --cwd /w/x --json -- claude")
}

func TestExecClient_RunWindow_Failures(t *testing.T) {
	tests := []struct {
		name    string
		fixture map[string]string
		wantErr string
	}{
		{name: "rex が失敗したら stderr を添える", fixture: map[string]string{"run.json": runJSON, "fail-run.json": ""}, wantErr: "server error"},
		{name: "block_ids が空なら pane が分からないので error", fixture: map[string]string{"run.json": `{"window_id":"window:x","block_ids":[],"revision":1}`}, wantErr: "block_ids"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeRex(t)
			for name, content := range tt.fixture {
				f.put(t, name, content)
			}
			c := f.client(staticResolver(nil))
			_, err := c.RunWindow(context.Background(), "", "/w/x", "", []string{"claude"})
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("RunWindow() error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}
