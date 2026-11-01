package depscli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/usadamasa/agents-daemon/internal/protocol"
)

// runTask は `task <sub>` に stdin を渡す。
func runTask(t *testing.T, sub, stdin string) (stdout, stderr string, err error) {
	t.Helper()
	cmd := newTaskCmd()
	var out, errBuf bytes.Buffer
	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	cmd.SetArgs([]string{sub})
	err = cmd.Execute()
	return out.String(), errBuf.String(), err
}

// writeDaemonStatus は動いている daemon (このテストプロセス) の pid と、記録より新しい status.json (pane は panes) を置く。
func writeDaemonStatus(t *testing.T, state, panes string) {
	t.Helper()
	pid := strconv.Itoa(os.Getpid())
	writeFile(t, filepath.Join(state, "daemon.pid"), pid+"\n")
	writeFile(t, filepath.Join(state, "status.json"), `{"pid": `+pid+`, "updated_at": "`+time.Now().Add(time.Minute).Format(time.RFC3339)+`", "panes": [`+panes+`]}`)
}

func writeTask(t *testing.T, state, session, id, status string) {
	t.Helper()
	writeFile(t, filepath.Join(state, "tasks", session, id+".json"),
		`{"id": "`+id+`", "subject": "秘密のタスク", "status": "`+status+`", "session_id": "`+session+`", "cwd": "/src/a", "updated_at": "2026-10-18T09:00:00Z"}`)
}

func dismiss(t *testing.T, refs ...string) protocol.TaskDismissed {
	t.Helper()
	in, err := json.Marshal(map[string]any{"refs": refs, "session_id": "viewer"})
	if err != nil {
		t.Fatal(err)
	}
	out, stderr, err := runTask(t, "dismiss", string(in))
	if err != nil {
		t.Fatalf("task dismiss: %v %s", err, stderr)
	}
	var res protocol.TaskDismissed
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("出力を読めない: %v\n%s", err, out)
	}
	return res
}

func reasons(res protocol.TaskDismissed) map[string]string {
	m := map[string]string{}
	for _, f := range res.Failures {
		m[f.Ref] = f.Reason
	}
	return m
}

// dismiss は終了したセッションのタスクの記録を、完了しているかを問わず消す。終了を確かめられないタスクと
// task: でない参照は消さずに failures に理由を返し、ほかの参照は続けて消す。
func TestTaskDismiss(t *testing.T) {
	root := testEnv(t)
	state := filepath.Join(root, "state", "agents-daemon")
	writeTask(t, state, "dead", "4", "pending")
	writeTask(t, state, "dead", "5", "completed")
	writeTask(t, state, "live", "1", "completed")
	exists := func(session, id string) bool {
		_, err := os.Stat(filepath.Join(state, "tasks", session, id+".json"))
		return err == nil
	}

	res := dismiss(t, "task:dead/4")
	if len(res.Dismissed) != 0 || !strings.Contains(reasons(res)["task:dead/4"], "daemon が動いていない") || !exists("dead", "4") {
		t.Fatalf("daemon が動いていないのに消した: %+v", res)
	}

	writeDaemonStatus(t, state, `{"pane_id": "b1", "session_id": "live", "cwd": "/src/a"}`)
	res = dismiss(t, "task:dead/4", "sid:dead", "task:live/1", "task:dead/5", "task:dead/9")
	if got := strings.Join(res.Dismissed, ","); got != "task:dead/4,task:dead/5" {
		t.Errorf("dismissed = %s, want task:dead/4,task:dead/5", got)
	}
	why := reasons(res)
	for ref, want := range map[string]string{"sid:dead": "task:<session_id>/<task_id>", "task:live/1": "生きている", "task:dead/9": "無い"} {
		if !strings.Contains(why[ref], want) {
			t.Errorf("%s の理由 = %q, want %q を含む", ref, why[ref], want)
		}
	}
	if exists("dead", "4") || exists("dead", "5") || !exists("live", "1") {
		t.Errorf("記録の残り方が違う: dead/4 %v、dead/5 %v、live/1 %v", exists("dead", "4"), exists("dead", "5"), exists("live", "1"))
	}

	// 消したタスクの参照と消さなかった理由をログに書き、subject は書かない。
	got := eventLines(t, state)
	if len(got) < 4 {
		t.Fatalf("ログ = %v", got)
	}
	equalLines(t, got[len(got)-4:], []string{
		`task.dismiss.failed session=viewer ref=sid:dead reason="参照 \"sid:dead\" は task:<session_id>/<task_id> の形でない"`,
		`task.dismiss.failed session=viewer ref=task:live/1 reason="タスク live/1 は消さない: セッション live は pane b1 で生きている"`,
		`task.dismiss.failed session=viewer ref=task:dead/9 reason="タスク dead/9 が無い"`,
		"task.dismiss session=viewer tasks=2 refs=task:dead/4,task:dead/5",
	})
	if strings.Contains(strings.Join(got, "\n"), "秘密") {
		t.Errorf("subject をログに書いた:\n%s", strings.Join(got, "\n"))
	}
}

// refs が空の入力は schema で弾く。
func TestTaskDismiss_rejectsEmptyRefs(t *testing.T) {
	testEnv(t)
	if _, _, err := runTask(t, "dismiss", `{"refs": []}`); err == nil {
		t.Fatal("refs が空の入力を受け付けた")
	}
}
