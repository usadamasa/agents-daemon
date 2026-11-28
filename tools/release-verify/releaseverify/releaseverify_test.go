package releaseverify_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/usadamasa/agents-daemon/tools/release-verify/releaseverify"
)

// fixture は、検証の対象の一時の repo と、複製元と、そこを指す設定。実データと本物の rex には触れない。
type fixture struct {
	t   *testing.T
	cfg releaseverify.Config
	out *bytes.Buffer
	log *bytes.Buffer
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	tmp, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// 呼び手の env に載っている rex の変数が、隔離の env へ漏れないことを確かめる。
	t.Setenv("REX_SESSION", "session:caller")
	t.Setenv("REX_BLOCK", "block:caller")

	repo := filepath.Join(tmp, "repo")
	stub, err := os.ReadFile(filepath.Join("testdata", "fakedaemon", "main.go.txt"))
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(repo, "go.mod"), "module fixture.invalid/agents-daemon\n\ngo 1.21\n")
	write(t, filepath.Join(repo, "cmd", "agents-daemon", "main.go"), string(stub))
	write(t, filepath.Join(repo, "gui", "app.txt"), "a\n")
	write(t, filepath.Join(repo, "hooks", "register.ts"), "a\n")
	write(t, filepath.Join(repo, "docs.txt"), "a\n")
	git(t, repo, "init", "-q", "-b", "main")
	git(t, repo, "config", "user.email", "test@example.invalid")
	git(t, repo, "config", "user.name", "test")
	git(t, repo, "config", "commit.gpgsign", "false")
	git(t, repo, "config", "tag.gpgsign", "false")
	git(t, repo, "add", ".")
	git(t, repo, "commit", "-q", "-m", "base")
	git(t, repo, "tag", "1.0.0")

	// 複製元。allowlist の中と外を 1 つずつ置く。
	state := filepath.Join(tmp, "src", "state")
	claude := filepath.Join(tmp, "src", "claude")
	write(t, filepath.Join(state, "work", "s1.json"), "{}\n")
	write(t, filepath.Join(state, "work", ".lock"), "x\n")
	write(t, filepath.Join(state, "edges", "e1.json"), "{}\n")
	write(t, filepath.Join(state, "spawned-sessions", "x.json"), "{}\n")
	write(t, filepath.Join(state, "logs", "daemon.log"), "log\n")
	write(t, filepath.Join(state, "daemon.pid"), "1\n")
	write(t, filepath.Join(claude, "projects", "p1", "memory", "MEMORY.md"), "memory\n")
	write(t, filepath.Join(claude, "projects", "p1", "session.jsonl"), "transcript\n")
	write(t, filepath.Join(claude, "plugins", "installed_plugins.json"), "{}\n")
	write(t, filepath.Join(claude, "CLAUDE.md"), "global\n")
	write(t, filepath.Join(tmp, "src", "skill-origin", "SKILL.md"), "skill\n")
	if err := os.MkdirAll(filepath.Join(claude, "skills"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(tmp, "src", "skill-origin"), filepath.Join(claude, "skills", "linked")); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(tmp, "src", "config", "config.json")
	write(t, config, "{\"enabled\":true}\n")
	socket := filepath.Join(tmp, "rex.sock")
	write(t, socket, "")

	f := &fixture{t: t, out: &bytes.Buffer{}, log: &bytes.Buffer{}}
	f.cfg = releaseverify.Config{
		Repo:         repo,
		Base:         filepath.Join(tmp, "base"),
		SourceState:  state,
		SourceClaude: claude,
		SourceConfig: config,
		BaselineBin:  filepath.Join(tmp, "no-baseline"),
		RealApp:      filepath.Join(tmp, "no-app"),
		RexSocket:    socket,
		Project:      repo,
		DaemonRun:    300 * time.Millisecond,
		Stdout:       f.out,
		Stderr:       f.log,
	}
	return f
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func git(t *testing.T, repo string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// run は 1 つのコマンドを回す。stdout と log は呼び出しごとに空にする。
func (f *fixture) run(command string, ref ...string) error {
	f.t.Helper()
	f.out.Reset()
	f.log.Reset()
	r := "HEAD"
	if len(ref) > 0 {
		r = ref[0]
	}
	return releaseverify.Run(context.Background(), f.cfg, command, r)
}

func (f *fixture) must(command string, ref ...string) {
	f.t.Helper()
	if err := f.run(command, ref...); err != nil {
		f.t.Fatalf("%s: %v\n%s", command, err, f.log)
	}
}

// root は ref の tree から決まる環境の root。
func (f *fixture) root(ref string) string {
	f.t.Helper()
	return filepath.Join(f.cfg.Base, git(f.t, f.cfg.Repo, "rev-parse", ref+"^{tree}")[:12])
}

// binDir は建てた代役の置き場。代役は calls と失敗の切り替えをここで読み書きする。
func (f *fixture) binDir() string {
	f.t.Helper()
	return filepath.Join(f.root("HEAD"), "home", ".cache", "agents-daemon", "bin")
}

// commitChange は gui/ と mod に触れない commit を 1 つ足す。
func (f *fixture) commitChange(content string) {
	f.t.Helper()
	write(f.t, filepath.Join(f.cfg.Repo, "docs.txt"), content+"\n")
	git(f.t, f.cfg.Repo, "commit", "-q", "-am", content)
}

type call struct {
	Args []string `json:"args"`
	Env  []string `json:"env"`
}

// callOf は、引数が prefix で始まる最後の呼び出しを返す。
func (f *fixture) callOf(prefix string) call {
	f.t.Helper()
	data, err := os.ReadFile(filepath.Join(f.binDir(), "calls"))
	if err != nil {
		f.t.Fatal(err)
	}
	var found *call
	for line := range strings.Lines(string(data)) {
		var c call
		if err := json.Unmarshal([]byte(line), &c); err != nil {
			f.t.Fatal(err)
		}
		if strings.HasPrefix(strings.Join(c.Args, " "), prefix) {
			found = &c
		}
	}
	if found == nil {
		f.t.Fatalf("%q の呼び出しが無い:\n%s", prefix, data)
	}
	return *found
}

func (f *fixture) status(ref ...string) (map[string]any, error) {
	f.t.Helper()
	err := f.run("status", ref...)
	var got map[string]any
	if jsonErr := json.Unmarshal(f.out.Bytes(), &got); jsonErr != nil {
		f.t.Fatalf("status の stdout が JSON ではない: %v\n%s", jsonErr, f.out)
	}
	if strings.Count(strings.TrimSpace(f.out.String()), "\n") != 0 {
		f.t.Fatalf("status の stdout が 1 行ではない:\n%s", f.out)
	}
	return got, err
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func wantExit(t *testing.T, err error, code int) {
	t.Helper()
	if got := releaseverify.ExitCode(err); got != code {
		t.Fatalf("exit code = %d、want %d (err: %v)", got, code, err)
	}
}

func wantRefusal(t *testing.T, err error, message string) {
	t.Helper()
	wantExit(t, err, 2)
	if !strings.Contains(err.Error(), message) {
		t.Fatalf("拒否の理由 = %q、want %q を含む", err, message)
	}
}

func TestUp_CopiesOnlyAllowlistAndKeepsSymlinks(t *testing.T) {
	f := newFixture(t)
	f.must("up")
	root := f.root("HEAD")
	state := filepath.Join(root, "home", ".local", "state", "agents-daemon")
	claude := filepath.Join(root, "home", ".claude")
	for _, p := range []string{
		filepath.Join(root, ".agents-daemon-verify"),
		filepath.Join(state, "work", "s1.json"),
		filepath.Join(state, "edges", "e1.json"),
		filepath.Join(claude, "projects", "p1", "memory", "MEMORY.md"),
		filepath.Join(claude, "plugins", "installed_plugins.json"),
		filepath.Join(claude, "CLAUDE.md"),
		filepath.Join(root, "home", ".config", "agents-daemon", "config.json"),
	} {
		if !exists(p) {
			t.Errorf("写っていない: %s", p)
		}
	}
	for _, p := range []string{
		filepath.Join(state, "work", ".lock"),
		filepath.Join(state, "spawned-sessions"),
		filepath.Join(state, "logs"),
		filepath.Join(state, "daemon.pid"),
		filepath.Join(claude, "projects", "p1", "session.jsonl"),
	} {
		if exists(p) {
			t.Errorf("写してはいけないものが写っている: %s", p)
		}
	}
	info, err := os.Lstat(filepath.Join(claude, "skills", "linked"))
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("skill の symlink を辿って写した: %v %v", info, err)
	}
}

func TestUp_BuildsFromCommitNotWorkingTree(t *testing.T) {
	f := newFixture(t)
	main := filepath.Join(f.cfg.Repo, "cmd", "agents-daemon", "main.go")
	src, err := os.ReadFile(main)
	if err != nil {
		t.Fatal(err)
	}
	write(t, main, strings.Replace(string(src), `builtFrom = "committed"`, `builtFrom = "uncommitted"`, 1))
	write(t, filepath.Join(f.cfg.Repo, "cmd", "extra.txt"), "uncommitted\n")
	f.must("up")
	f.must("check")
	root := f.root("HEAD")
	if exists(filepath.Join(root, "src", "cmd", "extra.txt")) {
		t.Error("未 commit のファイルが展開されている")
	}
	deps, err := os.ReadFile(filepath.Join(root, "report", "deps.out"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(deps), `"built_from":"committed"`) {
		t.Errorf("作業ツリーから建っている: %s", deps)
	}
}

func TestUp_RefusesExistingEnvironment(t *testing.T) {
	f := newFixture(t)
	f.must("up")
	wantRefusal(t, f.run("up"), "同じ tree の環境が残っている")
}

func TestUp_RefusesOverlapWithSources(t *testing.T) {
	t.Run("複製元が置き場の下", func(t *testing.T) {
		f := newFixture(t)
		f.cfg.Base = filepath.Dir(f.cfg.SourceState)
		wantRefusal(t, f.run("up"), "複製元が環境の置き場の下にある")
		if exists(f.root("HEAD")) {
			t.Error("拒んだのに環境を作った")
		}
	})
	t.Run("置き場が複製元の下", func(t *testing.T) {
		f := newFixture(t)
		f.cfg.Base = filepath.Join(f.cfg.SourceState, "verify")
		wantRefusal(t, f.run("up"), "環境の置き場が複製元の下にある")
	})
}

func TestCheck_CLIRunsInIsolatedEnv(t *testing.T) {
	f := newFixture(t)
	f.must("up")
	f.must("check")
	root := f.root("HEAD")
	home := filepath.Join(root, "home")
	got := f.callOf("deps").Env
	// LANG は呼び手の値を継ぐので、キーだけを見る。
	want := []string{
		"CFFIXED_USER_HOME=" + home,
		"CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"),
		"HOME=" + home,
		"PATH=/usr/bin:/bin:/usr/sbin:/sbin",
		"REX_SERVER=unix://" + filepath.Join(root, "rex-dead.sock"),
		"TMPDIR=" + filepath.Join(root, "tmp"),
		"XDG_CACHE_HOME=" + filepath.Join(home, ".cache"),
		"XDG_CONFIG_HOME=" + filepath.Join(home, ".config"),
		"XDG_STATE_HOME=" + filepath.Join(home, ".local", "state"),
	}
	got = slices.DeleteFunc(got, func(kv string) bool { return strings.HasPrefix(kv, "LANG=") })
	if !slices.Equal(got, want) {
		t.Errorf("CLI の env が許可した一覧と違う:\n got %q\nwant %q", got, want)
	}
}

func TestCheck_DaemonRunsDryAgainstRealRexAndClaude(t *testing.T) {
	f := newFixture(t)
	f.must("up")
	f.must("check")
	c := f.callOf("daemon")
	if !slices.Equal(c.Args, []string{"daemon", "--foreground", "--dry-run"}) {
		t.Errorf("daemon の引数 = %q", c.Args)
	}
	home := filepath.Join(f.root("HEAD"), "home")
	for _, kv := range []string{
		"XDG_STATE_HOME=" + filepath.Join(home, ".local", "state"),
		"CLAUDE_CONFIG_DIR=" + f.cfg.SourceClaude,
		"REX_SERVER=unix://" + f.cfg.RexSocket,
		"REX_BLOCK=block:release-verify",
	} {
		if !slices.Contains(c.Env, kv) {
			t.Errorf("daemon の env に %q が無い: %q", kv, c.Env)
		}
	}
	for _, kv := range c.Env {
		if strings.Contains(kv, "caller") || strings.HasPrefix(kv, "REX_SESSION=") {
			t.Errorf("呼び手の rex の変数か、rex がもう入れない REX_SESSION が入った: %q", kv)
		}
	}
}

func TestCheck_DaemonFailures(t *testing.T) {
	for name, toggle := range map[string]string{
		"秒数もたずに終わる": "daemon-exit-early",
		"panic を出す": "daemon-panic",
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			f.must("up")
			write(t, filepath.Join(f.binDir(), toggle), "")
			err := f.run("check")
			wantExit(t, err, 1)
			if !strings.Contains(f.log.String(), "[fail] daemon") {
				t.Errorf("daemon の項目が fail になっていない:\n%s", f.log)
			}
		})
	}
}

func TestCheck_OwnSkillsFindingsFail(t *testing.T) {
	f := newFixture(t)
	write(t, filepath.Join(f.cfg.Repo, "skills", "s1", "SKILL.md"), "skill\n")
	git(t, f.cfg.Repo, "add", "skills")
	git(t, f.cfg.Repo, "commit", "-q", "-m", "skill")
	f.must("up")
	f.must("check")
	c := f.callOf("dream check --json --memory-dir")
	if !slices.Contains(c.Args, filepath.Join(f.root("HEAD"), "src", "skills")) {
		t.Errorf("リリースする tree の skill の置き場を渡していない: %q", c.Args)
	}

	write(t, filepath.Join(f.binDir(), "fail-own-skills"), "")
	wantExit(t, f.run("check"), 1)
	if !strings.Contains(f.log.String(), "[fail] dream-check-skills") {
		t.Errorf("skill の項目が fail になっていない:\n%s", f.log)
	}
}

func TestCheck_MissingRexSocketFails(t *testing.T) {
	f := newFixture(t)
	if err := os.Remove(f.cfg.RexSocket); err != nil {
		t.Fatal(err)
	}
	f.must("up")
	wantExit(t, f.run("check"), 1)
}

func TestCheck_ChangedRealEnvironmentFails(t *testing.T) {
	f := newFixture(t)
	f.must("up")
	write(t, f.cfg.SourceConfig, "{\"enabled\":false}\n")
	wantExit(t, f.run("check"), 1)
	if !strings.Contains(f.log.String(), "[fail] untouched") {
		t.Errorf("常用の環境の変化を fail にしていない:\n%s", f.log)
	}
}

func TestRun_RecordsAndRemovesEnvironment(t *testing.T) {
	f := newFixture(t)
	f.commitChange("b")
	f.must("run")
	if exists(f.root("HEAD")) {
		t.Error("通ったのに環境が残っている")
	}
	got, err := f.status()
	wantExit(t, err, 0)
	if got["result"] != "pass" || got["tree"] != git(t, f.cfg.Repo, "rev-parse", "HEAD^{tree}") {
		t.Errorf("status = %v", got)
	}
}

func TestRun_KeepLeavesEnvironment(t *testing.T) {
	f := newFixture(t)
	f.cfg.Keep = true
	f.must("run")
	if !exists(f.root("HEAD")) {
		t.Error("Keep なのに環境を消した")
	}
}

func TestRun_FailedCheckWritesNoRecordAndKeepsEnvironment(t *testing.T) {
	f := newFixture(t)
	f.commitChange("b")
	f.must("up")
	write(t, filepath.Join(f.binDir(), "fail-deps"), "")
	wantExit(t, f.run("check"), 1)
	if !strings.Contains(f.log.String(), "[fail] deps") {
		t.Errorf("deps の項目が fail になっていない:\n%s", f.log)
	}
	if !exists(f.root("HEAD")) {
		t.Error("落ちたのに環境を消した")
	}
	got, err := f.status()
	wantExit(t, err, 1)
	if got["result"] != "unverified" {
		t.Errorf("status = %v", got)
	}
}

func TestStatus_Freshness(t *testing.T) {
	f := newFixture(t)
	f.commitChange("b")
	f.must("run")

	git(t, f.cfg.Repo, "commit", "-q", "--allow-empty", "-m", "merge の代わり")
	if _, err := f.status(); err != nil {
		t.Errorf("commit が違っても tree が同じなら通る: %v", err)
	}

	f.commitChange("c")
	got, err := f.status()
	wantExit(t, err, 1)
	if got["result"] != "unverified" {
		t.Errorf("commit が増えたのに未検証に戻らない: %v", got)
	}
	if _, err := f.status("HEAD~1"); err != nil {
		t.Errorf("ref を引数で受けない: %v", err)
	}
}

func TestStatus_HumanRequiredForGUIAndModChanges(t *testing.T) {
	for name, path := range map[string]string{"gui": "gui/app.txt", "mod": "hooks/register.ts"} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			f.cfg.Keep = true
			write(t, filepath.Join(f.cfg.Repo, filepath.FromSlash(path)), "b\n")
			git(t, f.cfg.Repo, "commit", "-q", "-am", name)
			f.must("run")
			got, err := f.status()
			wantExit(t, err, 1)
			if got["result"] != "human_required" {
				t.Fatalf("status = %v", got)
			}
			f.must("approve")
			got, err = f.status()
			wantExit(t, err, 0)
			if got["result"] != "pass" {
				t.Errorf("approve の後の status = %v", got)
			}
		})
	}
}

func TestStatus_HumanRequiredWithoutPreviousTag(t *testing.T) {
	f := newFixture(t)
	git(t, f.cfg.Repo, "tag", "-d", "1.0.0")
	f.must("run")
	got, err := f.status()
	wantExit(t, err, 1)
	if got["result"] != "human_required" {
		t.Errorf("status = %v", got)
	}
}

func TestApprove_RefusesWithoutMachinePass(t *testing.T) {
	f := newFixture(t)
	f.commitChange("b")
	f.must("up")
	wantRefusal(t, f.run("approve"), "機械の点検が通った記録が無い")
}

func TestDown_RemovesEnvironment(t *testing.T) {
	f := newFixture(t)
	f.must("up")
	f.must("down")
	if exists(f.root("HEAD")) {
		t.Error("環境が残っている")
	}
	f.must("down")
}

func TestDown_RefusesForeignDirectories(t *testing.T) {
	t.Run("印が無い", func(t *testing.T) {
		f := newFixture(t)
		precious := filepath.Join(f.root("HEAD"), "precious")
		write(t, precious, "keep\n")
		wantRefusal(t, f.run("down"), "印 (.agents-daemon-verify) が無い")
		if !exists(precious) {
			t.Error("印の無いディレクトリを消した")
		}
	})
	t.Run("root が symlink", func(t *testing.T) {
		f := newFixture(t)
		elsewhere := filepath.Join(t.TempDir(), "elsewhere")
		marker := filepath.Join(elsewhere, ".agents-daemon-verify")
		write(t, marker, "{}\n")
		if err := os.MkdirAll(f.cfg.Base, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(elsewhere, f.root("HEAD")); err != nil {
			t.Fatal(err)
		}
		wantRefusal(t, f.run("down"), "symlink")
		if !exists(marker) {
			t.Error("symlink の先を消した")
		}
	})
	t.Run("印の tree が違う", func(t *testing.T) {
		f := newFixture(t)
		marker := filepath.Join(f.root("HEAD"), ".agents-daemon-verify")
		write(t, marker, "{\"tree\":\"0000\"}\n")
		wantRefusal(t, f.run("down"), "印の tree が ref の tree と違う")
		if !exists(marker) {
			t.Error("別の tree の環境を消した")
		}
	})
}

func TestRun_UnknownCommandIsRefused(t *testing.T) {
	f := newFixture(t)
	err := f.run("nope")
	wantExit(t, err, 2)
	if _, ok := errors.AsType[*releaseverify.RefusalError](err); !ok {
		t.Errorf("拒否の error ではない: %v", err)
	}
}
