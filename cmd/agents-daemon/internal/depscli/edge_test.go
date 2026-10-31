package depscli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/usadamasa/agents-daemon/internal/edge"
	"github.com/usadamasa/agents-daemon/internal/gitrepo"
)

// runEdge は edge に stdin と引数を渡す。add と rm は stdin の JSON で、list は flag で受ける。
func runEdge(t *testing.T, stdin string, args ...string) (stdout string, err error) {
	t.Helper()
	cmd := newEdgeCmd()
	var out, errBuf bytes.Buffer
	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	cmd.SetArgs(args)
	err = cmd.Execute()
	return out.String(), err
}

// target は辺の端点の JSON。
func target(ref, cwd string) map[string]string {
	return map[string]string{"ref": ref, "cwd": cwd}
}

func edgeReq(t *testing.T, m map[string]any) string {
	t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
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
	root := testEnv(t)
	repo, wt := testRepo(t, filepath.Join(root, "src", "home", "app"))
	other := filepath.Join(root, "src", "home", "other")
	mkdirs(t, other)
	ends := map[string]any{"before": target("session:ad-134", wt), "after": target("issue:usadamasa/agents-daemon#135", other)}
	withNote := map[string]any{"before": ends["before"], "after": ends["after"], "note": "段 4 の後"}

	out, err := runEdge(t, edgeReq(t, withNote), "add")
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
	out, err = runEdge(t, edgeReq(t, ends), "add")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(out), &added); err != nil || added.Created || added.Edge.ID != e.ID {
		t.Errorf("二重登録 = %+v, %v", added, err)
	}

	list := func(args ...string) []edge.Edge {
		t.Helper()
		out, err := runEdge(t, "", append([]string{"list"}, args...)...)
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

	out, err = runEdge(t, edgeReq(t, ends), "rm")
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

// task 欄の端点は書く側のセッション自身のタスク。参照は task:<CLAUDE_CODE_SESSION_ID>/<id> をバイナリが組み、cwd を
// 省くとバイナリの cwd になる。rm は task 欄でも組んだ参照でも同じ辺に当たる。
func TestEdgeAdd_ownTask(t *testing.T) {
	root := testEnv(t)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "SELF")
	peer := filepath.Join(root, "src", "home", "peer")
	mkdirs(t, peer)
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	add := func(m map[string]any) edge.Added {
		t.Helper()
		out, err := runEdge(t, edgeReq(t, m), "add")
		if err != nil {
			t.Fatalf("edge add %v: %v", m, err)
		}
		var added edge.Added
		if err := json.Unmarshal([]byte(out), &added); err != nil {
			t.Fatalf("add の JSON を読めない: %v\n%s", err, out)
		}
		return added
	}
	rm := func(m map[string]any) edge.Removed {
		t.Helper()
		out, err := runEdge(t, edgeReq(t, m), "rm")
		if err != nil {
			t.Fatalf("edge rm %v: %v", m, err)
		}
		var removed edge.Removed
		if err := json.Unmarshal([]byte(out), &removed); err != nil {
			t.Fatalf("rm の JSON を読めない: %v\n%s", err, out)
		}
		return removed
	}

	// 自分のタスクが peer を待つ向き。cwd を省く。
	waits := map[string]any{"before": target("session:peer-b", peer), "after": map[string]string{"task": "3"}, "note": "peer-b の PR の後"}
	got := add(waits).Edge
	if got.After.Ref != "task:SELF/3" || got.After.CWD != wd || got.After.Repo != gitrepo.RepoKey(wd) || got.Before.Ref != "session:peer-b" {
		t.Errorf("task 欄の端点 = %+v", got)
	}
	// cwd を明示すればそれを使う。
	explicit := map[string]any{"before": map[string]string{"task": "4", "cwd": peer}, "after": target("pr:usadamasa/agents-daemon#1", peer)}
	if e := add(explicit).Edge; e.Before.Ref != "task:SELF/4" || e.Before.CWD != peer {
		t.Errorf("cwd を明示した task 欄の端点 = %+v", e)
	}

	if r := rm(map[string]any{"before": target("session:peer-b", peer), "after": map[string]string{"task": "3"}}); !r.Removed || r.ID != got.ID {
		t.Errorf("task 欄で rm = %+v", r)
	}
	if r := rm(map[string]any{"before": target("task:SELF/4", peer), "after": target("pr:usadamasa/agents-daemon#1", peer)}); !r.Removed {
		t.Errorf("組んだ参照で rm = %+v", r)
	}
}

// task 欄は ref と同時に書けず、どちらかは要る。CLAUDE_CODE_SESSION_ID が無ければ組めない。
func TestEdgeAdd_ownTaskRefused(t *testing.T) {
	root := testEnv(t)
	home := filepath.Join(root, "src", "home", "a")
	mkdirs(t, home)
	other := target("session:b", home)
	for name, m := range map[string]map[string]any{
		"ref と task の両方":   {"before": map[string]string{"ref": "session:a", "cwd": home, "task": "1"}, "after": other},
		"ref も task も無い":   {"before": map[string]string{"cwd": home}, "after": other},
		"task が空":          {"before": map[string]string{"task": ""}, "after": other},
		"task に / がある":     {"before": map[string]string{"task": "1/2"}, "after": other},
		"task の cwd が相対パス": {"before": map[string]string{"task": "1", "cwd": "rel"}, "after": other},
	} {
		t.Setenv("CLAUDE_CODE_SESSION_ID", "SELF")
		if _, err := runEdge(t, edgeReq(t, m), "add"); err == nil {
			t.Errorf("%s: edge add が通った", name)
		}
	}
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	m := map[string]any{"before": map[string]string{"task": "1"}, "after": other}
	if _, err := runEdge(t, edgeReq(t, m), "add"); err == nil || !strings.Contains(err.Error(), "CLAUDE_CODE_SESSION_ID") {
		t.Errorf("env 無し: err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "state", "agents-daemon", "edges")); !os.IsNotExist(err) {
		t.Errorf("何か書かれた (err=%v)", err)
	}
}

// 端点の cwd は両方要る。
func TestEdgeAdd_refused(t *testing.T) {
	root := testEnv(t)
	home := filepath.Join(root, "src", "home", "a")
	mkdirs(t, home)
	for name, m := range map[string]map[string]any{
		"after の cwd が無い": {"before": target("session:a", home), "after": map[string]string{"ref": "session:b"}},
		"参照の種類が範囲外":       {"before": target("branch:a", home), "after": target("session:b", home)},
		"flag の形の欄":       {"before": "session:a", "before-cwd": home, "after": "session:b", "after-cwd": home},
	} {
		if _, err := runEdge(t, edgeReq(t, m), "add"); err == nil {
			t.Errorf("%s: edge add が通った", name)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "state", "agents-daemon", "edges")); !os.IsNotExist(err) {
		t.Errorf("何か書かれた (err=%v)", err)
	}
}
