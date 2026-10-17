package statusline

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// initRepo は空リポジトリを 1 コミット済みで作る (branch 検証の前提)。
func initRepo(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.email", "t@example.com"},
		{"config", "user.name", "t"},
		{"commit", "-q", "--allow-empty", "-m", "init"},
	} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

func TestGitBranch(t *testing.T) {
	t.Run("リポジトリでは branch 名", func(t *testing.T) {
		repo := filepath.Join(t.TempDir(), "repo")
		initRepo(t, repo)
		if out, err := exec.Command("git", "-C", repo, "checkout", "-q", "-b", "feature-x").CombinedOutput(); err != nil {
			t.Fatalf("checkout: %v\n%s", err, out)
		}
		if got := GitBranch(repo); got != "feature-x" {
			t.Errorf("GitBranch() = %q, want feature-x", got)
		}
	})
	t.Run("detached なら (detached)", func(t *testing.T) {
		repo := filepath.Join(t.TempDir(), "repo")
		initRepo(t, repo)
		if out, err := exec.Command("git", "-C", repo, "checkout", "-q", "--detach").CombinedOutput(); err != nil {
			t.Fatalf("checkout: %v\n%s", err, out)
		}
		if got := GitBranch(repo); got != "(detached)" {
			t.Errorf("GitBranch() = %q, want (detached)", got)
		}
	})
	t.Run("git 管理外と空のパスは空", func(t *testing.T) {
		if got := GitBranch(t.TempDir()); got != "" {
			t.Errorf("GitBranch(非 git) = %q, want empty", got)
		}
		if got := GitBranch(""); got != "" {
			t.Errorf("GitBranch(\"\") = %q, want empty", got)
		}
	})
}

func TestProjectBranch(t *testing.T) {
	// 起動元が git 管理外でも 🏠 は出すので、branch の欄は (detached) で埋める。
	if got := ProjectBranch(t.TempDir()); got != "(detached)" {
		t.Errorf("ProjectBranch(非 git) = %q, want (detached)", got)
	}
}

func TestOutsideProject(t *testing.T) {
	tests := []struct {
		cur, project string
		want         bool
	}{
		{"/w/proj", "/w/proj", false},
		{"/w/proj/sub", "/w/proj", false},
		{"/w/away", "/w/proj", true},
		{"/w/foobar", "/w/foo", true},
		{"/w/proj", "", false},
		{"", "/w/proj", false},
	}
	for _, tc := range tests {
		if got := OutsideProject(tc.cur, tc.project); got != tc.want {
			t.Errorf("OutsideProject(%q, %q) = %v, want %v", tc.cur, tc.project, got, tc.want)
		}
	}
}

func TestReadEffort(t *testing.T) {
	t.Run("settings.json の effortLevel", func(t *testing.T) {
		home := t.TempDir()
		writeSettings(t, home, `{"effortLevel": "high"}`)
		if got := ReadEffort(home); got != "high" {
			t.Errorf("ReadEffort() = %q, want high", got)
		}
	})
	t.Run("ファイルが無い・キーが無い・壊れているなら空", func(t *testing.T) {
		if got := ReadEffort(t.TempDir()); got != "" {
			t.Errorf("ReadEffort(無し) = %q", got)
		}
		home := t.TempDir()
		writeSettings(t, home, `{}`)
		if got := ReadEffort(home); got != "" {
			t.Errorf("ReadEffort(キー無し) = %q", got)
		}
		writeSettings(t, home, `{`)
		if got := ReadEffort(home); got != "" {
			t.Errorf("ReadEffort(壊れた JSON) = %q", got)
		}
	})
}

func writeSettings(t *testing.T, home, content string) {
	t.Helper()
	dir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
