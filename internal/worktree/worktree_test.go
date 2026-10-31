package worktree

// 「誰も使っていない worktree だけを回収し、それ以外 (稼働中・自分・新しい・未コミットあり・
// 人手の lock・subagent の lock・管理外パス) は残す」を、temp dir の実 git で検証する。

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// oldTime は経過日数の判定を決定的にするための固定の過去日。
var oldTime = time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)

// git は dir で git を実行し stdout を返す。失敗はテストの失敗にする。
func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	return gitEnv(t, dir, nil, args...)
}

// gitEnv は環境変数を足して git を実行する (コミット日時の固定に使う)。
func gitEnv(t *testing.T, dir string, env []string, args ...string) string {
	t.Helper()
	full := append([]string{"-C", dir, "-c", "user.name=test", "-c", "user.email=test@test.com"}, args...)
	cmd := exec.Command("git", full...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	cmd.Env = append(cmd.Env, env...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, stderr.String())
	}
	return strings.TrimSpace(string(out))
}

// oldDateEnv はコミット日時を oldTime に固定する環境変数。
func oldDateEnv() []string {
	iso := oldTime.Format(time.RFC3339)
	return []string{"GIT_AUTHOR_DATE=" + iso, "GIT_COMMITTER_DATE=" + iso}
}

// newRepo は main worktree を作り、HEAD のコミット日時を過去に固定する。
// 経過日数は worktree ディレクトリと管理 index の mtime だけで決まるので、テストごとに backdate で制御できる。
func newRepo(t *testing.T) string {
	t.Helper()
	repo := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(repo, 0o750); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "init", "-q", "-b", "main")
	gitEnv(t, repo, oldDateEnv(), "commit", "-q", "--allow-empty", "-m", "initial")
	return repo
}

func worktreePath(repo, name string) string {
	return filepath.Join(repo, ".claude", "worktrees", name)
}

// addWorktree は .claude/worktrees/<name> に worktree を作る。
func addWorktree(t *testing.T, repo, name, branch string) string {
	t.Helper()
	git(t, repo, "worktree", "add", "-q", worktreePath(repo, name), "-b", branch)
	return worktreePath(repo, name)
}

// backdate は worktree の最終更新 (ルートの mtime と管理 index の mtime) を過去にする。
func backdate(t *testing.T, repo, name string) {
	t.Helper()
	wt := worktreePath(repo, name)
	admin := git(t, wt, "rev-parse", "--absolute-git-dir")
	for _, p := range []string{filepath.Join(admin, "index"), wt} {
		if err := os.Chtimes(p, oldTime, oldTime); err != nil {
			t.Fatal(err)
		}
	}
}

// lockAs は Claude Code と同じ書式の reason で lock する。kind は "session" か "agent"。
func lockAs(t *testing.T, repo, name, kind string, pid int) {
	t.Helper()
	reason := "claude " + kind + " " + name + " (pid " + strconv.Itoa(pid) + " start Sat Aug 15 04:34:58 2026)"
	git(t, repo, "worktree", "lock", "--reason", reason, worktreePath(repo, name))
}

// deadPID は確実に存在しない PID を返す (起動してすぐ終わったプロセスの PID)。
func deadPID(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	return cmd.Process.Pid
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func registered(t *testing.T, repo, name string) bool {
	t.Helper()
	return strings.Contains(git(t, repo, "worktree", "list", "--porcelain"), "worktrees/"+name)
}

// run は cwd を起点に既定のしきい値で回収を回し、出力を返す。生存判定は全 PID を不在とみなす (決定的にするため)。
func run(t *testing.T, cwd string, mod func(*Opts)) string {
	t.Helper()
	var out bytes.Buffer
	opts := Opts{Cwd: cwd, MinAge: DefaultMinAge, Stdout: &out, Alive: func(int) bool { return false }}
	if mod != nil {
		mod(&opts)
	}
	if _, err := Run(opts); err != nil {
		t.Fatalf("Run: %v\n%s", err, out.String())
	}
	return out.String()
}

// Run の返り値は、文字で報告した worktree ごとの判定と同じものを持つ (--json が出す)。
// 黙って飛ばすもの (新しい、subagent の lock) は文字と同じく載らない。
func TestRun_ReturnsDecisions(t *testing.T) {
	repo := newRepo(t)
	stale := addWorktree(t, repo, "stale", "wt-stale")
	backdate(t, repo, "stale")
	dirty := addWorktree(t, repo, "dirty", "wt-dirty")
	if err := os.WriteFile(filepath.Join(dirty, "untracked.txt"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	backdate(t, repo, "dirty")
	manual := addWorktree(t, repo, "manual", "wt-manual")
	git(t, repo, "worktree", "lock", "--reason", "作業中につき保護", manual)
	backdate(t, repo, "manual")
	addWorktree(t, repo, "fresh", "wt-fresh")

	for _, tt := range []struct {
		dryRun     bool
		wantStale  Action
		wantRemove bool
	}{{true, ActionWouldRemove, false}, {false, ActionRemoved, true}} {
		res, err := Run(Opts{Cwd: repo, MinAge: DefaultMinAge, DryRun: tt.dryRun, Alive: func(int) bool { return false }})
		if err != nil {
			t.Fatal(err)
		}
		got := map[string]Decision{}
		for _, d := range res.Worktrees {
			got[filepath.Base(d.Path)] = d
		}
		if len(got) != 3 {
			t.Errorf("dryRun=%v: worktrees = %+v, want stale・dirty・manual の 3 つ", tt.dryRun, res.Worktrees)
		}
		if d := got["stale"]; d.Action != tt.wantStale || d.Reason != ReasonStale || d.AgeDays < 7 {
			t.Errorf("dryRun=%v: stale = %+v", tt.dryRun, d)
		}
		if d := got["dirty"]; d.Action != ActionKept || d.Reason != ReasonDirty {
			t.Errorf("dryRun=%v: dirty = %+v", tt.dryRun, d)
		}
		if d := got["manual"]; d.Action != ActionKept || d.Reason != ReasonForeignLock || d.Lock != "作業中につき保護" {
			t.Errorf("dryRun=%v: manual = %+v", tt.dryRun, d)
		}
		if exists(stale) == tt.wantRemove {
			t.Errorf("dryRun=%v: stale の有無 = %v", tt.dryRun, exists(stale))
		}
	}
}

// ---- 回収するケース ----

func TestRun_RemovesUnlockedStaleWorktree(t *testing.T) {
	repo := newRepo(t)
	wt := addWorktree(t, repo, "stale", "wt-stale")
	backdate(t, repo, "stale")

	run(t, repo, nil)

	if exists(wt) {
		t.Error("stale worktree が残っている")
	}
	if registered(t, repo, "stale") {
		t.Error("stale が worktree list に残っている")
	}
}

func TestRun_UnlocksAndRemovesDeadSessionWorktree(t *testing.T) {
	repo := newRepo(t)
	wt := addWorktree(t, repo, "stale", "wt-stale")
	lockAs(t, repo, "stale", "session", deadPID(t))
	backdate(t, repo, "stale")

	run(t, repo, nil)

	if exists(wt) {
		t.Error("死んだセッションの worktree が残っている")
	}
}

func TestRun_KeepsBranch(t *testing.T) {
	repo := newRepo(t)
	addWorktree(t, repo, "stale", "wt-stale")
	backdate(t, repo, "stale")

	run(t, repo, nil)

	if !strings.Contains(git(t, repo, "branch", "--list", "wt-stale"), "wt-stale") {
		t.Error("ブランチが消えている")
	}
}

func TestRun_RemovesWorktreeWithUnpushedCommits(t *testing.T) {
	repo := newRepo(t)
	wt := addWorktree(t, repo, "ahead", "wt-ahead")
	gitEnv(t, wt, oldDateEnv(), "commit", "-q", "--allow-empty", "-m", "work")
	backdate(t, repo, "ahead")

	run(t, repo, nil)

	if exists(wt) {
		t.Error("push 前のコミットがある worktree が残っている (ブランチに残るので回収してよい)")
	}
	if !strings.Contains(git(t, repo, "log", "--oneline", "wt-ahead"), "work") {
		t.Error("コミットがブランチから消えている")
	}
}

func TestRun_PrunesMissingDirectoryRegardlessOfAge(t *testing.T) {
	repo := newRepo(t)
	wt := addWorktree(t, repo, "ghost", "wt-ghost")
	lockAs(t, repo, "ghost", "session", deadPID(t))
	if err := os.RemoveAll(wt); err != nil {
		t.Fatal(err)
	}

	run(t, repo, nil)

	if registered(t, repo, "ghost") {
		t.Error("実体の無い登録が残っている")
	}
}

func TestRun_ForceRemovesDirtyWorktree(t *testing.T) {
	repo := newRepo(t)
	wt := addWorktree(t, repo, "dirty", "wt-dirty")
	if err := os.WriteFile(filepath.Join(wt, "untracked.txt"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	backdate(t, repo, "dirty")

	run(t, repo, func(o *Opts) { o.Force = true })

	if exists(wt) {
		t.Error("--force でも dirty worktree が残っている")
	}
}

func TestRun_MinAgeZeroRemovesFreshWorktree(t *testing.T) {
	repo := newRepo(t)
	wt := addWorktree(t, repo, "fresh", "wt-fresh")

	run(t, repo, func(o *Opts) { o.MinAge = 0 })

	if exists(wt) {
		t.Error("--min-age-days 0 なのに作りたての worktree が残っている")
	}
}

func TestRun_FromInsideWorktreeUsesMainAsRoot(t *testing.T) {
	repo := newRepo(t)
	stale := addWorktree(t, repo, "stale", "wt-stale")
	here := addWorktree(t, repo, "here", "wt-here")
	backdate(t, repo, "stale")

	run(t, here, nil)

	if exists(stale) {
		t.Error("worktree の中から実行したとき stale が回収されていない")
	}
	if !exists(here) {
		t.Error("自分がいる worktree が消えている")
	}
}

// ---- 残すケース ----

func TestRun_KeepsFreshWorktreeByDefault(t *testing.T) {
	repo := newRepo(t)
	wt := addWorktree(t, repo, "fresh", "wt-fresh")

	run(t, repo, nil)

	if !exists(wt) {
		t.Error("作りたての worktree が消えている")
	}
}

func TestRun_KeepsWhenIndexIsFresh(t *testing.T) {
	repo := newRepo(t)
	wt := addWorktree(t, repo, "nested", "wt-nested")
	if err := os.MkdirAll(filepath.Join(wt, "deep"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, "deep", "note.txt"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	git(t, wt, "add", "-A")
	gitEnv(t, wt, oldDateEnv(), "commit", "-q", "-m", "nested")
	// ルートだけ過去に戻す。index は git が今の時刻で書いたまま。
	if err := os.Chtimes(wt, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}

	run(t, repo, nil)

	if !exists(wt) {
		t.Error("index が新しい worktree が消えている")
	}
}

func TestRun_KeepsWhenHeadIsRecent(t *testing.T) {
	repo := newRepo(t)
	wt := addWorktree(t, repo, "recent", "wt-recent")
	git(t, wt, "commit", "-q", "--allow-empty", "-m", "work")
	backdate(t, repo, "recent")

	run(t, repo, nil)

	if !exists(wt) {
		t.Error("HEAD が新しい worktree が消えている")
	}
}

func TestRun_KeepsLiveSessionWorktree(t *testing.T) {
	repo := newRepo(t)
	wt := addWorktree(t, repo, "live", "wt-live")
	lockAs(t, repo, "live", "session", 4242)
	backdate(t, repo, "live")

	run(t, repo, func(o *Opts) { o.Alive = func(pid int) bool { return pid == 4242 } })

	if !exists(wt) {
		t.Error("生きているセッションの worktree が消えている")
	}
}

func TestRun_KeepsOwnWorktree(t *testing.T) {
	repo := newRepo(t)
	here := addWorktree(t, repo, "here", "wt-here")
	backdate(t, repo, "here")

	run(t, here, nil)

	if !exists(here) {
		t.Error("自分がいる worktree が古いというだけで消えている")
	}
}

func TestRun_KeepsDirtyWorktreeAndMentionsForce(t *testing.T) {
	repo := newRepo(t)
	wt := addWorktree(t, repo, "dirty", "wt-dirty")
	if err := os.WriteFile(filepath.Join(wt, "untracked.txt"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	backdate(t, repo, "dirty")

	out := run(t, repo, nil)

	if !exists(wt) {
		t.Error("未コミットの変更がある worktree が消えている")
	}
	if !strings.Contains(out, "--force") {
		t.Errorf("--force の案内が無い: %q", out)
	}
}

func TestRun_KeepsForeignLock(t *testing.T) {
	repo := newRepo(t)
	wt := addWorktree(t, repo, "manual", "wt-manual")
	git(t, repo, "worktree", "lock", "--reason", "作業中につき保護", wt)
	backdate(t, repo, "manual")

	out := run(t, repo, nil)

	if !exists(wt) {
		t.Error("人手の lock の worktree が消えている")
	}
	if !strings.Contains(out, "manual") {
		t.Errorf("人手の lock を飛ばした旨の出力が無い: %q", out)
	}
}

func TestRun_KeepsLockWithoutReason(t *testing.T) {
	repo := newRepo(t)
	wt := addWorktree(t, repo, "bare", "wt-bare")
	git(t, repo, "worktree", "lock", wt)
	backdate(t, repo, "bare")

	run(t, repo, nil)

	if !exists(wt) {
		t.Error("理由なしの lock の worktree が消えている")
	}
}

// cc-premise: 2.1.302 worktrees subagent の worktree は `claude agent <name> (pid N ...)` で lock され、本体の sweep が cleanupPeriodDays で消す
func TestRun_LeavesAgentLockToClaudeCodeSweep(t *testing.T) {
	repo := newRepo(t)
	wt := addWorktree(t, repo, "sub", "wt-sub")
	lockAs(t, repo, "sub", "agent", deadPID(t))
	backdate(t, repo, "sub")

	out := run(t, repo, nil)

	if !exists(wt) {
		t.Error("subagent の worktree を消している (本体の sweep の担当)")
	}
	if out != "" {
		t.Errorf("subagent の worktree について出力している (毎セッション systemMessage になる): %q", out)
	}
}

func TestRun_IgnoresWorktreeOutsideClaudeDir(t *testing.T) {
	repo := newRepo(t)
	outside := filepath.Join(filepath.Dir(repo), "outside")
	git(t, repo, "worktree", "add", "-q", outside, "-b", "wt-outside")
	if err := os.Chtimes(outside, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}

	run(t, repo, nil)

	if !exists(outside) {
		t.Error(".claude/worktrees/ の外の worktree が消えている")
	}
}

func TestRun_DryRunRemovesNothing(t *testing.T) {
	repo := newRepo(t)
	wt := addWorktree(t, repo, "stale", "wt-stale")
	backdate(t, repo, "stale")

	out := run(t, repo, func(o *Opts) { o.DryRun = true })

	if !exists(wt) {
		t.Error("--dry-run で消している")
	}
	if !strings.Contains(out, "stale") {
		t.Errorf("回収対象の表示が無い: %q", out)
	}
}

func TestRun_QuietWhenNothingToDo(t *testing.T) {
	repo := newRepo(t)
	addWorktree(t, repo, "fresh", "wt-fresh")

	out := run(t, repo, nil)

	if out != "" {
		t.Errorf("回収するものが無いのに出力がある: %q", out)
	}
}

// ---- 入力の扱い ----

func TestRun_OutsideRepositoryIsError(t *testing.T) {
	_, err := Run(Opts{Cwd: t.TempDir(), MinAge: DefaultMinAge, Stdout: &bytes.Buffer{}})
	if !errors.Is(err, ErrNotRepository) {
		t.Errorf("err = %v; want ErrNotRepository", err)
	}
}

// ---- lock reason の解釈 ----

// cc-premise: 2.1.302 worktrees lock reason は `claude <session|agent> <name> (pid N[ start <procStart>])`。start は省かれることがある
func TestParseLockReason(t *testing.T) {
	cases := []struct {
		reason string
		kind   lockKind
		pid    int
	}{
		{"claude session wt-a (pid 123 start Sat Aug 15 04:34:58 2026)", LockSession, 123},
		{"claude session wt-a (pid 123)", LockSession, 123},
		{"claude agent sub-1 (pid 55 start 1760000000)", LockAgent, 55},
		{"claude agent sub-1 (pid 55)", LockAgent, 55},
		{"claude session wt-a", LockForeign, 0},
		{"作業中につき保護", LockForeign, 0},
		{"", LockForeign, 0},
	}
	for _, c := range cases {
		got := parseLockReason(c.reason)
		if got.Kind != c.kind || got.PID != c.pid {
			t.Errorf("ParseLockReason(%q) = %+v; want kind=%v pid=%d", c.reason, got, c.kind, c.pid)
		}
	}
}

// ---- 生存判定 ----

func TestProcessAlive(t *testing.T) {
	if !processAlive(os.Getpid()) {
		t.Error("自分自身を不在と判定した")
	}
	if processAlive(deadPID(t)) {
		t.Error("終了したプロセスを生存と判定した")
	}
}
