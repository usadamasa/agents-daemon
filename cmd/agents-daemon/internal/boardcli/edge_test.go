package boardcli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/usadamasa/agents-daemon/internal/edge"
	"github.com/usadamasa/agents-daemon/internal/gitrepo"
)

func runEdge(t *testing.T, args ...string) (stdout string, err error) {
	t.Helper()
	cmd := newEdgeCmd()
	var out, errBuf bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	cmd.SetArgs(args)
	err = cmd.Execute()
	return out.String(), err
}

func mkdirs(t *testing.T, dirs ...string) {
	t.Helper()
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o750); err != nil {
			t.Fatal(err)
		}
	}
}

// add は両端の cwd から repo を決めて辺を書く。linked worktree の cwd も main worktree の repo になる。
// 同じ辺の二重登録は created = false で、辺は増えない。
func TestEdgeAddListRm(t *testing.T) {
	root := parkTestEnv(t)
	repo, wt := parkTestRepo(t, filepath.Join(root, "src", "home", "app"))
	other := filepath.Join(root, "src", "home", "other")
	mkdirs(t, other)
	args := []string{"--before", "session:ad-134", "--before-cwd", wt, "--after", "issue:usadamasa/agents-daemon#135", "--after-cwd", other}

	out, err := runEdge(t, append([]string{"add", "--note", "段 4 の後"}, args...)...)
	if err != nil {
		t.Fatalf("edge add: %v", err)
	}
	var added edge.Added
	if err := json.Unmarshal([]byte(out), &added); err != nil {
		t.Fatalf("add の JSON を読めない: %v\n%s", err, out)
	}
	e := added.Edge
	if !added.Created || e.Before.Repo != gitrepo.RepoKey(repo) || e.After.Repo != other || e.Note != "段 4 の後" {
		t.Errorf("added = %+v", added)
	}
	out, err = runEdge(t, append([]string{"add"}, args...)...)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(out), &added); err != nil || added.Created || added.Edge.ID != e.ID {
		t.Errorf("二重登録 = %+v, %v", added, err)
	}

	list := func(args ...string) []edge.Edge {
		t.Helper()
		out, err := runEdge(t, append([]string{"list"}, args...)...)
		if err != nil {
			t.Fatalf("edge list %v: %v", args, err)
		}
		var edges []edge.Edge
		if err := json.Unmarshal([]byte(out), &edges); err != nil {
			t.Fatalf("list の JSON を読めない: %v\n%s", err, out)
		}
		return edges
	}
	if got := list("--cwd", other); len(got) != 1 || got[0].ID != e.ID {
		t.Errorf("list = %+v", got)
	}
	if got := list("--here", "--cwd", wt); len(got) != 1 {
		t.Errorf("--here (linked worktree) = %+v", got)
	}
	if got := list("--cwd", other, "--ref", "session:nobody"); got == nil || len(got) != 0 {
		t.Errorf("--ref で絞れていない: %+v", got)
	}

	out, err = runEdge(t, append([]string{"rm"}, args...)...)
	if err != nil {
		t.Fatalf("edge rm: %v", err)
	}
	var removed edge.Removed
	if err := json.Unmarshal([]byte(out), &removed); err != nil || !removed.Removed || removed.ID != e.ID {
		t.Errorf("rm = %+v, %v\n%s", removed, err, out)
	}
	if got := list("--cwd", other); len(got) != 0 {
		t.Errorf("消えていない: %+v", got)
	}
}

// 端点の cwd は両方要る。
func TestEdgeAdd_refused(t *testing.T) {
	root := parkTestEnv(t)
	home := filepath.Join(root, "src", "home", "a")
	mkdirs(t, home)
	for name, args := range map[string][]string{
		"after-cwd が無い": {"--before", "session:a", "--before-cwd", home, "--after", "session:b"},
		"参照の種類が範囲外":     {"--before", "branch:a", "--before-cwd", home, "--after", "session:b", "--after-cwd", home},
	} {
		if _, err := runEdge(t, append([]string{"add"}, args...)...); err == nil {
			t.Errorf("%s: edge add が通った", name)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "state", "agents-daemon", "edges")); !os.IsNotExist(err) {
		t.Errorf("何か書かれた (err=%v)", err)
	}
}
