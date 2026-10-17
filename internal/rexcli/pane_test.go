package rexcli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/usadamasa/agents-daemon/internal/multiplexer"
)

// testdata の実測のディレクトリ。中身の由来は testdata/README.md。
const (
	measuredOct10 = "2026-10-10_rex-0.0.1791570264_cc-2.1.296"
	measuredOct17 = "2026-10-17_rex-preview.15_cc-2.1.302"
)

// 実測の fixture の owner.pid。foreground の process の pid と突き合わせる。
const (
	pidClaudeWorking    = 15470
	pidClaudeDone       = 17633
	pidClaudePermission = 94252
	pidClaudeStartup    = 70904
	pidShell            = 72817
)

// 実測に無い形の program_status。
const (
	psEmpty = `{"records":[]}`
	// event payload の root record は "id":"" で来る。
	psEmptyID = `{"records":[{"id":"","state":"working","app":"claude-code","updated_at":"2026-10-10T18:30:41.644437+09:00","owner":{"pid":15470,"name":"2.1.296"}}]}`
	// owner は foreground と一致させ、root と取り違えたときだけ working になるようにする。
	psSubagentOnly = `{"records":[{"id":"a01b73b4e3800b31f","state":"working","app":"claude-code","updated_at":"2026-10-10T18:29:58.836427+09:00","owner":{"pid":15470,"name":"2.1.296"}}]}`
	psUnknownState = `{"records":[{"state":"starting","app":"claude-code","updated_at":"2026-10-10T18:29:58.836427+09:00","owner":{"pid":15470,"name":"2.1.296"}}]}`
	psOtherApp     = `{"records":[{"state":"working","app":"cargo","effective_app":"cargo","updated_at":"2026-10-17T14:00:00.000000+09:00","owner":{"pid":15470,"name":"cargo"}}]}`
	// rex が owner を付けなくなった (または名前を変えた) ときの形。
	psNoOwner = `{"records":[{"state":"done","app":"claude-code","effective_app":"claude-code","updated_at":"2026-10-17T14:00:00.000000+09:00"}]}`
)

// measured は testdata の実測の応答を返す。
func measured(t *testing.T, dir, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", dir, name)) // #nosec G304 -- テストの fixture
	if err != nil {
		t.Fatalf("fixture %s/%s の読み込みに失敗: %v", dir, name, err)
	}
	return string(b)
}

// processJSON は `block call process` の応答。2026-10-10 の実測の pid と argv0 と cwd を差し替える。
func processJSON(pid int, argv0, cwd string) string {
	return `{"last_exit":null,` +
		`"child":{"pid":33929,"user_id":0,"user":"root","name":"login","path":"/usr/bin/login"},` +
		`"foreground":{"pid":` + strconv.Itoa(pid) + `,"user_id":501,"user":"usadamasa","name":"2.1.296","path":"/Users/usadamasa/.local/share/claude/versions/2.1.296","invoked_path":"/Users/usadamasa/.local/bin/claude","argv0":"` + argv0 + `","cwd":"` + cwd + `"},` +
		`"ancestors":[{"pid":33930,"user_id":501,"user":"usadamasa","name":"zsh","path":"/bin/zsh","invoked_path":"/bin/zsh","argv0":"-zsh","cwd":"` + cwd + `"}],` +
		`"home":"/Users/usadamasa"}`
}

func TestToPane(t *testing.T) {
	const (
		reasonNoOwner    = "record に owner.pid が無い (rex の版が変わった可能性。record を使えず agent_status が unknown のままになる)"
		reasonStaleOwner = "record の owner (pid 73002) が foreground の process (pid 70904) と違う (前の Claude の record が残っている)"
	)
	staleDone := measured(t, measuredOct17, "program_status-stale-done.json")
	tests := []struct {
		name       string
		ps         string
		pid        int
		argv0      string
		wantAgent  string
		wantStatus multiplexer.AgentStatus
		wantReason string
	}{
		// state の写像。working の fixture は subagent の record も持つ。
		{"subagent と並ぶ root の working", measured(t, measuredOct10, "program_status-working.json"), pidClaudeWorking, "claude", "claude", multiplexer.AgentStatusWorking, ""},
		{"ターン終了の done", measured(t, measuredOct10, "program_status-done.json"), pidClaudeDone, "claude", "claude", multiplexer.AgentStatusDone, ""},
		{"起動直後の idle", measured(t, measuredOct17, "program_status-startup.json"), pidClaudeStartup, "claude", "claude", multiplexer.AgentStatusIdle, ""},
		{"permission prompt は blocked", measured(t, measuredOct10, "program_status-permission.json"), pidClaudePermission, "claude", "claude", multiplexer.AgentStatusBlocked, ""},
		{"未知の state は unknown", psUnknownState, pidClaudeWorking, "claude", "claude", multiplexer.AgentStatusUnknown, ""},
		// root record の選び方。
		{"event 形の id 空の root record", psEmptyID, pidClaudeWorking, "claude", "claude", multiplexer.AgentStatusWorking, ""},
		{"subagent の record だけなら状態を取らない", psSubagentOnly, pidClaudeWorking, "claude", "claude", multiplexer.AgentStatusUnknown, ""},
		// record を使えないときの argv0 による判定。--shell=none で起動した block では argv0 が絶対パス。
		{"record が無くても argv0 の basename が claude なら Claude", psEmpty, pidClaudeStartup, "/Users/usadamasa/.local/bin/claude", "claude", multiplexer.AgentStatusUnknown, ""},
		{"shell だけの block", psEmpty, pidShell, "-zsh", "", multiplexer.AgentStatusUnknown, ""},
		{"Claude が死んで done の record だけ残った shell", staleDone, pidShell, "-zsh", "", multiplexer.AgentStatusUnknown, ""},
		{"前の Claude の record が残る中で claude を起動し直した直後", staleDone, pidClaudeStartup, "claude", "claude", multiplexer.AgentStatusUnknown, reasonStaleOwner},
		{"claude-code 以外の app の record", psOtherApp, pidClaudeWorking, "cargo", "", multiplexer.AgentStatusUnknown, ""},
		{"rex が owner を付けなくなったら理由を残す", psNoOwner, pidClaudeWorking, "claude", "claude", multiplexer.AgentStatusUnknown, reasonNoOwner},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var ps programStatusResult
			if err := json.Unmarshal([]byte(tt.ps), &ps); err != nil {
				t.Fatalf("program_status の parse に失敗: %v", err)
			}
			var proc processResult
			if err := json.Unmarshal([]byte(processJSON(tt.pid, tt.argv0, "/w")), &proc); err != nil {
				t.Fatalf("process の parse に失敗: %v", err)
			}
			got := toPane("block:x", ps, proc, "sid")
			want := multiplexer.Pane{
				PaneID: "block:x", TerminalID: "block:x", CWD: "/w",
				Agent: tt.wantAgent, AgentStatus: tt.wantStatus, SessionID: "sid", UnknownReason: tt.wantReason,
			}
			if got != want {
				t.Errorf("toPane() = %+v, want %+v", got, want)
			}
		})
	}
}
