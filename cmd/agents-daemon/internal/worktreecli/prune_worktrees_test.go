package worktreecli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/usadamasa/agents-daemon/cmd/agents-daemon/internal/jsonflag"
	"github.com/usadamasa/agents-daemon/internal/worktree"
)

// testRoot は main の root と同じく SilenceUsage を立てた親にコマンドを載せる。
func testRoot() *cobra.Command {
	root := &cobra.Command{Use: "agents-daemon", SilenceUsage: true}
	root.AddCommand(Commands()...)
	return root
}

// pruneTestRepo は main と、過去に戻した放置 worktree 1 つ (`.claude/worktrees/stale`) を持つリポジトリを作る。
// 判定の網羅は worktree パッケージのテストが持ち、ここでは flag の配線だけを見る。
func pruneTestRepo(t *testing.T) (repo, stale string) {
	t.Helper()
	repo = filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(repo, 0o750); err != nil {
		t.Fatal(err)
	}
	old := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	dateEnv := []string{"GIT_AUTHOR_DATE=" + old.Format(time.RFC3339), "GIT_COMMITTER_DATE=" + old.Format(time.RFC3339)}
	run := func(env []string, args ...string) string {
		full := append([]string{"-C", repo, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)
		cmd := exec.Command("git", full...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		cmd.Env = append(cmd.Env, env...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run(nil, "init", "-q", "-b", "main")
	run(dateEnv, "commit", "-q", "--allow-empty", "-m", "initial")
	stale = filepath.Join(repo, ".claude", "worktrees", "stale")
	run(nil, "worktree", "add", "-q", stale, "-b", "wt-stale")
	admin := run(nil, "-C", stale, "rev-parse", "--absolute-git-dir")
	for _, p := range []string{filepath.Join(admin, "index"), stale} {
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatal(err)
		}
	}
	return repo, stale
}

func execPrune(t *testing.T, stdin string, args ...string) (stdout string, err error) {
	t.Helper()
	var out bytes.Buffer
	root := testRoot()
	root.SetOut(&out)
	root.SetIn(strings.NewReader(stdin))
	root.SetArgs(append([]string{"prune-worktrees"}, args...))
	err = root.Execute()
	return out.String(), err
}

func TestPruneWorktreesCmd_HasFlags(t *testing.T) {
	sub, _, err := testRoot().Find([]string{"prune-worktrees"})
	if err != nil || sub.Name() != "prune-worktrees" {
		t.Fatalf("Find(prune-worktrees) = %v, %v", sub, err)
	}
	for _, f := range []string{"dry-run", "force", "min-age-days", "hook"} {
		if sub.Flags().Lookup(f) == nil {
			t.Errorf("--%s が無い", f)
		}
	}
}

func TestPruneWorktreesCmd_RunsInCwd(t *testing.T) {
	repo, stale := pruneTestRepo(t)
	t.Chdir(repo)

	out, err := execPrune(t, "")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if _, err := os.Stat(stale); err == nil {
		t.Error("stale worktree が残っている")
	}
	if !strings.Contains(out, "stale") {
		t.Errorf("回収の報告が無い: %q", out)
	}
}

func TestPruneWorktreesCmd_DryRunKeeps(t *testing.T) {
	repo, stale := pruneTestRepo(t)
	t.Chdir(repo)

	if _, err := execPrune(t, "", "--dry-run"); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if _, err := os.Stat(stale); err != nil {
		t.Error("--dry-run で消している")
	}
}

func TestPruneWorktreesCmd_OutsideRepositoryFails(t *testing.T) {
	t.Chdir(t.TempDir())

	if _, err := execPrune(t, ""); err == nil {
		t.Error("git リポジトリの外で成功した")
	}
}

func TestPruneWorktreesCmd_HookReadsCwdFromStdin(t *testing.T) {
	repo, stale := pruneTestRepo(t)
	t.Chdir(t.TempDir())

	input, _ := json.Marshal(map[string]string{"cwd": repo})
	out, err := execPrune(t, string(input), "--hook")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if _, err := os.Stat(stale); err == nil {
		t.Error("stale worktree が残っている")
	}
	if !strings.Contains(out, `"systemMessage"`) {
		t.Errorf("systemMessage が無い: %q", out)
	}
}

// --json は worktree.Result を出し、文字の報告は混ぜない。--hook の stdout は Claude Code が読む systemMessage なので
// --json と併用すると error。
func TestPruneWorktreesCmd_JSON(t *testing.T) {
	repo, stale := pruneTestRepo(t)
	t.Chdir(repo)

	execJSON := func(args ...string) (string, error) {
		var out bytes.Buffer
		root := testRoot()
		jsonflag.Add(root)
		root.SetOut(&out)
		root.SetIn(strings.NewReader(""))
		root.SetArgs(append([]string{"--json", "prune-worktrees"}, args...))
		err := root.Execute()
		return out.String(), err
	}

	out, err := execJSON("--dry-run")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var res worktree.Result
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("JSON = %q: %v", out, err)
	}
	if len(res.Worktrees) != 1 || res.Worktrees[0].Action != worktree.ActionWouldRemove || !strings.HasSuffix(res.Worktrees[0].Path, "stale") {
		t.Errorf("worktrees = %+v, want stale の would_remove 1 つ", res.Worktrees)
	}
	if _, err := os.Stat(stale); err != nil {
		t.Error("--dry-run で消している")
	}

	if _, err := execJSON("--hook"); err == nil || !strings.Contains(err.Error(), "--json") {
		t.Errorf("--hook: error = %v, want --json に対応していない error", err)
	}
}

func TestPruneWorktreesCmd_RejectsNegativeMinAge(t *testing.T) {
	repo, _ := pruneTestRepo(t)
	t.Chdir(repo)

	if _, err := execPrune(t, "", "--min-age-days", "-1"); err == nil {
		t.Error("負の --min-age-days を受け付けた")
	}
}
