package parkcli

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/usadamasa/agents-daemon/internal/spawnedsession"
)

func runSpawnedSession(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := newSpawnedSessionCmd()
	var out bytes.Buffer
	cmd.SetIn(strings.NewReader(""))
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

// `spawned-session mark <id>` は state に印を書き、書いた印を JSON で出す。spawn と peer-sessions skill の手順が
// 起こした session の中から呼ぶ (#557)。
func TestSpawnedSessionMark(t *testing.T) {
	root := parkTestEnv(t)
	const id = "session:01a1ca60-c767-7aaa-857b-7e167825e668"
	out, err := runSpawnedSession(t, "mark", id)
	if err != nil {
		t.Fatalf("mark error = %v", err)
	}
	var got spawnedsession.Record
	if err := json.Unmarshal([]byte(out), &got); err != nil || got.SessionID != id || got.CreatedAt.IsZero() {
		t.Errorf("stdout = %q (%v), want session_id %q と書いた時刻", out, err, id)
	}
	recs, err := spawnedsession.New(filepath.Join(root, "state", "agents-daemon")).List()
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 || recs[0].SessionID != id {
		t.Errorf("印 = %+v, want %q の 1 つ", recs, id)
	}
}

// session ID は 1 つだけ受ける。空の ID は書かない。
func TestSpawnedSessionMark_Args(t *testing.T) {
	parkTestEnv(t)
	for _, args := range [][]string{{"mark"}, {"mark", "a", "b"}, {"mark", ""}} {
		if _, err := runSpawnedSession(t, args...); err == nil {
			t.Errorf("%q: error = nil, want error", args)
		}
	}
}
