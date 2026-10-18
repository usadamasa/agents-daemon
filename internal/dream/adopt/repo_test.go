package adopt

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// git を使う test は git コマンドが必要
func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
}

// initGitRepo は tmp ディレクトリを git init する｡
func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	for _, args := range [][]string{
		{"init", "-b", "main"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "test"},
		{"commit", "--allow-empty", "-m", "initial"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

func TestFindRepoRoot_FromFileInsideRepo(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	initGitRepo(t, dir)
	// nested file
	nested := filepath.Join(dir, "a", "b", "c.txt")
	_ = os.MkdirAll(filepath.Dir(nested), 0o755)
	_ = os.WriteFile(nested, []byte("x"), 0o644)

	root, err := findRepoRoot(nested)
	if err != nil {
		t.Fatalf("findRepoRoot: %v", err)
	}
	// filepath.EvalSymlinks で macOS の /private/tmp/... prefix が挟まる可能性を吸収
	wantResolved, _ := filepath.EvalSymlinks(dir)
	gotResolved, _ := filepath.EvalSymlinks(root)
	if gotResolved != wantResolved {
		t.Errorf("root = %q; want %q", gotResolved, wantResolved)
	}
}

func TestFindRepoRoot_NonGitReturnsError(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	_, err := findRepoRoot(filepath.Join(dir, "x"))
	if err == nil {
		t.Error("expected error for non-git path")
	}
}

func TestBranchName_UsesDateAndCandidateID(t *testing.T) {
	got := branchName("2026-07-20", "dream-candidate-daily-standup-001")
	want := "dream/2026-07-20-daily-standup-001"
	if got != want {
		t.Errorf("branchName = %q; want %q", got, want)
	}
}

func TestBranchName_HandlesCandidateWithoutSuffix(t *testing.T) {
	got := branchName("2026-07-20", "unusual-id")
	want := "dream/2026-07-20-unusual-id"
	if got != want {
		t.Errorf("branchName = %q; want %q", got, want)
	}
}
