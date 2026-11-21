package parkcli

import (
	"testing"

	"github.com/usadamasa/agents-daemon/internal/protocol"
	"github.com/usadamasa/agents-daemon/internal/spawn"
)

const callerRexSession = "session:01a1f64b-341c-7372-b66e-223d962c18be"

// spawn サブコマンドは stdin の欄をそのまま渡し、起こす側の rex session (REX_SESSION) を足し先の既定にする。
func TestSpawnCommandRequest(t *testing.T) {
	t.Setenv("REX_SESSION", callerRexSession)
	in := protocol.SpawnRequest{
		Repo: "/r/a", PromptFile: "./tmp/brief.md", Session: "session:kept", NewSession: true,
		SessionID: "0f2e9c4d-5a6b-4c7d-8e9f-a0b1c2d3e4f5", Model: "sonnet", Label: "fix-x",
		ForkFrom: "1a2b3c4d-5e6f-4a7b-8c9d-0e1f2a3b4c5d",
	}
	want := spawn.Request{
		Repo: "/r/a", PromptFile: "./tmp/brief.md", Session: "session:kept", NewSession: true,
		SessionID: "0f2e9c4d-5a6b-4c7d-8e9f-a0b1c2d3e4f5", Model: "sonnet", Label: "fix-x", Caller: callerRexSession,
		ForkFrom: "1a2b3c4d-5e6f-4a7b-8c9d-0e1f2a3b4c5d",
	}
	if got := spawnCommandRequest(in); got != want {
		t.Errorf("spawnCommandRequest() = %+v, want %+v", got, want)
	}
}

// park hand は rex の中のセッションから呼ばれても REX_SESSION を読まず、今までどおり新しい rex session に起こす。
func TestHandSpawnRequest_IgnoresRexSession(t *testing.T) {
	t.Setenv("REX_SESSION", callerRexSession)
	want := spawn.Request{Repo: "/r/a", Prompt: "本文"}
	if got := handSpawnRequest("/r/a", "本文"); got != want {
		t.Errorf("handSpawnRequest() = %+v, want %+v (足し先の指定を持たない)", got, want)
	}
}
