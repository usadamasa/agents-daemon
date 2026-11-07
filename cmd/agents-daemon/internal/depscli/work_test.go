package depscli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/usadamasa/agents-daemon/internal/protocol"
	"github.com/usadamasa/agents-daemon/internal/worklog"
)

// runWork は `work <sub>` に stdin を渡す。
func runWork(t *testing.T, sub, stdin string) (stdout string, err error) {
	t.Helper()
	cmd := newWorkCmd()
	var out bytes.Buffer
	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{sub})
	err = cmd.Execute()
	return out.String(), err
}

// runIngestWork は ingest-work を stdin 付きで実行する。
func runIngestWork(t *testing.T, stdin string) (stdout, stderr string) {
	t.Helper()
	cmd := newIngestWorkCmd()
	var out, errBuf bytes.Buffer
	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	cmd.SetArgs([]string{})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v (失敗しても exit 0 のはず)", err)
	}
	return out.String(), errBuf.String()
}

// ingest-work は work/<session_id>.json に書き、stdout には何も出さない。記録を作ったときと終了したときだけ出来事を書く。
func TestIngestWorkCmd(t *testing.T) {
	root := testEnv(t)
	state := filepath.Join(root, "state", "agents-daemon")
	prompt := `{"session_id": "s1", "cwd": "/w", "hook_event_name": "UserPromptSubmit", "prompt": "README を直す"}`
	for _, in := range []string{prompt, prompt, `{"session_id": "s1", "cwd": "/w", "hook_event_name": "Stop", "last_assistant_message": "直した"}`, `{"session_id": "s1", "hook_event_name": "SessionEnd", "reason": "other"}`} {
		if stdout, stderr := runIngestWork(t, in); stdout != "" || stderr != "" {
			t.Errorf("stdout / stderr = %q / %q, want 空", stdout, stderr)
		}
	}
	rec, ok := worklog.New(state).Get("s1")
	if !ok || rec.Subject != "README を直す" || rec.Turns != 2 || rec.Summary != "直した" || !worklog.Ended(rec) {
		t.Errorf("記録 = %+v, %v", rec, ok)
	}
	equalLines(t, eventLines(t, state), []string{
		"ingest-work session=s1 event=UserPromptSubmit created=true",
		"ingest-work session=s1 event=SessionEnd ended=true",
	})

	stdout, stderr := runIngestWork(t, `{"session_id": `)
	if stdout != "" || !strings.Contains(stderr, "ingest-work:") {
		t.Errorf("stdout / stderr = %q / %q", stdout, stderr)
	}
	if got := eventLines(t, state); len(got) != 3 || !strings.HasPrefix(got[2], "ingest-work.failed ") {
		t.Errorf("失敗の出来事 = %v", got)
	}
}

// work set は題を書いて Work を出し、題そのものはログに書かない。work get は記録を出し、無ければ error。
func TestWorkCmd(t *testing.T) {
	root := testEnv(t)
	state := filepath.Join(root, "state", "agents-daemon")
	if _, err := runWork(t, "get", `{"session_id": "s1"}`); err == nil || !strings.Contains(err.Error(), "記録が無い") {
		t.Errorf("get (無い) = %v", err)
	}
	// 出来事のログの並びを確かめるので、順序のある slice で回す。
	for _, c := range []struct{ stdin, want string }{
		{`{"session_id": "s1"}`, "anyOf"},
		{`{"session_id": "s1", "subject": ""}`, "minLength"},
		{`{"session_id": "s1", "subject": "x"}`, "記録が無い"},
		{`{"session_id": "s1", "inferred": "x"}`, "記録が無い"},
		{`{"session_id": "s1", "subject": "x", "y": 1}`, "y"},
	} {
		if _, err := runWork(t, "set", c.stdin); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("set %s: error = %v, want %q を含む", c.stdin, err, c.want)
		}
	}
	runIngestWork(t, `{"session_id": "s1", "cwd": "/w", "hook_event_name": "UserPromptSubmit", "prompt": "最初の prompt"}`)

	out, err := runWork(t, "set", `{"session_id": "s1", "inferred": "推定の題"}`)
	if err != nil {
		t.Fatal(err)
	}
	var rec protocol.Work
	if err := json.Unmarshal([]byte(out), &rec); err != nil || rec.Inferred != "推定の題" || rec.Subject != "最初の prompt" {
		t.Errorf("set の出力 = %s (%v)", out, err)
	}
	out, err = runWork(t, "set", `{"session_id": "s1", "subject": "人の題"}`)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(out), &rec); err != nil || rec.Subject != "人の題" || rec.Origin != worklog.OriginHuman {
		t.Errorf("set の出力 = %s (%v)", out, err)
	}
	out, err = runWork(t, "get", `{"session_id": "s1"}`)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(out), &rec); err != nil || rec.Subject != "人の題" || rec.Inferred != "推定の題" || rec.Turns != 1 {
		t.Errorf("get の出力 = %s (%v)", out, err)
	}
	got := eventLines(t, state)
	equalLines(t, got, []string{
		`work.set.failed session=s1 origin=human reason="セッション s1 の作業の記録が無い"`,
		`work.set.failed session=s1 inferred=true reason="セッション s1 の作業の記録が無い"`,
		"ingest-work session=s1 event=UserPromptSubmit created=true",
		"work.set session=s1 inferred=true",
		"work.set session=s1 origin=human",
	})
	if strings.Contains(strings.Join(got, "\n"), "の題") {
		t.Errorf("題をログに書いた:\n%s", strings.Join(got, "\n"))
	}
	if _, err := os.Stat(filepath.Join(state, "work", "s1.json")); err != nil {
		t.Error(err)
	}
}
