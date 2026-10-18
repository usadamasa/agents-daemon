package worktree

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// runHook は hook 入力 input で RunHook を回し、stdout と stderr を返す。
func runHook(t *testing.T, input string, mod func(*Opts)) (stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	opts := Opts{MinAge: DefaultMinAge, Alive: func(int) bool { return false }}
	if mod != nil {
		mod(&opts)
	}
	if err := RunHook(strings.NewReader(input), &out, &errOut, opts); err != nil {
		t.Fatalf("RunHook はセッションを止めない (err = %v)", err)
	}
	return out.String(), errOut.String()
}

func cwdInput(t *testing.T, cwd string) string {
	t.Helper()
	b, err := json.Marshal(map[string]string{"cwd": cwd})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestRunHook_UsesCwdFromInputAndReportsSystemMessage(t *testing.T) {
	repo := newRepo(t)
	wt := addWorktree(t, repo, "stale", "wt-stale")
	backdate(t, repo, "stale")
	t.Chdir(t.TempDir()) // プロセスの cwd に頼らないことを見る

	out, _ := runHook(t, cwdInput(t, repo), nil)

	if exists(wt) {
		t.Error("stale worktree が残っている")
	}
	var msg struct {
		SystemMessage string `json:"systemMessage"`
	}
	if err := json.Unmarshal([]byte(out), &msg); err != nil {
		t.Fatalf("stdout が JSON でない: %q (%v)", out, err)
	}
	if !strings.Contains(msg.SystemMessage, "stale") {
		t.Errorf("systemMessage に回収の報告が無い: %q", msg.SystemMessage)
	}
}

func TestRunHook_SilentWhenNothingToDo(t *testing.T) {
	repo := newRepo(t)
	addWorktree(t, repo, "fresh", "wt-fresh")

	out, errOut := runHook(t, cwdInput(t, repo), nil)

	if out != "" || errOut != "" {
		t.Errorf("回収するものが無いのに出力がある: stdout=%q stderr=%q", out, errOut)
	}
}

func TestRunHook_OutsideRepositoryIsSilent(t *testing.T) {
	out, errOut := runHook(t, cwdInput(t, t.TempDir()), nil)

	if out != "" || errOut != "" {
		t.Errorf("git リポジトリの外で出力がある: stdout=%q stderr=%q", out, errOut)
	}
}

func TestRunHook_ToleratesNonJSONInput(t *testing.T) {
	t.Chdir(t.TempDir())

	out, _ := runHook(t, "not json at all", nil)

	if out != "" {
		t.Errorf("stdout がある: %q", out)
	}
}

func TestRunHook_FallsBackToProcessCwdWhenInputCwdIsMissing(t *testing.T) {
	repo := newRepo(t)
	wt := addWorktree(t, repo, "stale", "wt-stale")
	backdate(t, repo, "stale")
	t.Chdir(repo)

	runHook(t, cwdInput(t, "/nonexistent/dir"), nil)

	if exists(wt) {
		t.Error("入力の cwd が無いときにプロセスの cwd へ落ちていない")
	}
}
