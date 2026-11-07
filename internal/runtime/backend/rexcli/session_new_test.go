package rexcli

import (
	"context"
	"strings"
	"testing"
)

// sessionNewJSON は `session new --json` の応答。形は 2026-11-07 の実測。
const sessionNewJSON = `{"session_id":"session:01a1b37a-0560-743d-adfd-959e5e1c53a9","initial_windows":[{"window_id":"window:01a1b37a-056e-70fa-90fd-15d50af005a9","block_ids":["block:01a1b37a-0560-74ce-af88-d37d23de33b9"],"revision":1}]}`

// TestExecClient_NewSession は新しい rex session でコマンドを起こす配線を見る。起こす側の session を指さず (-s を付けない)、
// 終了後も block を残し、argv はそのまま渡す (空白を含む引数も 1 つのまま。改行を含む引数が 1 つのまま子へ届くことは
// fake の calls.log が行で割れるのでここでは見ず、実機で確かめた)。
func TestExecClient_NewSession(t *testing.T) {
	f := newFakeRex(t)
	f.put(t, "session-new.json", sessionNewJSON)
	c := f.client(staticResolver(nil))

	prompt := "パーキングロットのメモ「x」をタスクにしてください。 詳細: a b"
	got, err := c.NewSession(context.Background(), "claude-config", "/w/claude-config", []string{"claude", "--session-id", sidDone, "--", prompt})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	if want := "block:01a1b37a-0560-74ce-af88-d37d23de33b9"; got != want {
		t.Errorf("NewSession() = %q, want %q", got, want)
	}
	f.assertCalls(t,
		"--autostart=false session new claude-config --cwd /w/claude-config --keep-open --window claude-config --json -- claude --session-id "+sidDone+" -- "+shellQuote(prompt),
	)
}

// rex は `-` で始まる LABEL を flag と読んで拒むので (実測)、label の先頭の `-` を外す。外して空なら label を付けない。
func TestExecClient_NewSession_DashLabel(t *testing.T) {
	tests := []struct {
		label string
		want  string
	}{
		{label: "-x", want: "--autostart=false session new x --cwd /w/-x --keep-open --window x --json -- claude"},
		{label: "--", want: "--autostart=false session new --cwd /w/-x --keep-open --json -- claude"},
	}
	for _, tt := range tests {
		t.Run(tt.label, func(t *testing.T) {
			f := newFakeRex(t)
			f.put(t, "session-new.json", sessionNewJSON)
			c := f.client(staticResolver(nil))
			if _, err := c.NewSession(context.Background(), tt.label, "/w/-x", []string{"claude"}); err != nil {
				t.Fatalf("NewSession() error = %v", err)
			}
			f.assertCalls(t, tt.want)
		})
	}
}

func TestExecClient_NewSession_Failures(t *testing.T) {
	tests := []struct {
		name    string
		fixture map[string]string
		wantErr string
	}{
		{name: "rex が失敗したら stderr を添える", fixture: map[string]string{"session-new.json": sessionNewJSON, "fail-session-new.json": ""}, wantErr: "server error"},
		{name: "window が無ければ pane が分からないので error", fixture: map[string]string{"session-new.json": `{"session_id":"session:x","initial_windows":[]}`}, wantErr: "block_ids"},
		{name: "block_ids が空なら pane が分からないので error", fixture: map[string]string{"session-new.json": `{"session_id":"session:x","initial_windows":[{"window_id":"window:x","block_ids":[],"revision":1}]}`}, wantErr: "block_ids"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeRex(t)
			for name, content := range tt.fixture {
				f.put(t, name, content)
			}
			c := f.client(staticResolver(nil))
			_, err := c.NewSession(context.Background(), "x", "/w/x", []string{"claude"})
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("NewSession() error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}
