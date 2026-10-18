package forget

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newMemoryDir は memory ディレクトリを模した一時ディレクトリを作る｡
func newMemoryDir(t *testing.T, index string, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	if index != "" {
		if err := os.WriteFile(filepath.Join(dir, "MEMORY.md"), []byte(index), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestRunDeletesNamedMemoryFile(t *testing.T) {
	dir := newMemoryDir(t, "", map[string]string{
		"obsolete.md": "body",
		"keeper.md":   "body",
	})

	if err := Run(Opts{MemoryDir: dir, Names: []string{"obsolete"}}); err != nil {
		t.Fatalf("Run() = %v; want nil", err)
	}

	if _, err := os.Stat(filepath.Join(dir, "obsolete.md")); !os.IsNotExist(err) {
		t.Errorf("obsolete.md still exists (stat err = %v); want removed", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "keeper.md")); err != nil {
		t.Errorf("keeper.md = %v; want left intact", err)
	}
}

func TestRunRemovesIndexEntryFromMemoryMd(t *testing.T) {
	index := strings.Join([]string{
		"## 設定・ハーネス",
		"",
		"- [obsolete.md](obsolete.md) — 消える知見",
		"- [keeper.md](keeper.md) — 残る知見",
		"",
	}, "\n")
	dir := newMemoryDir(t, index, map[string]string{
		"obsolete.md": "body",
		"keeper.md":   "body",
	})

	if err := Run(Opts{MemoryDir: dir, Names: []string{"obsolete"}}); err != nil {
		t.Fatalf("Run() = %v; want nil", err)
	}

	got, err := os.ReadFile(filepath.Join(dir, "MEMORY.md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got), "obsolete.md") {
		t.Errorf("MEMORY.md still references obsolete.md:\n%s", got)
	}
	if !strings.Contains(string(got), "keeper.md") {
		t.Errorf("MEMORY.md lost the keeper.md entry:\n%s", got)
	}
	if !strings.Contains(string(got), "## 設定・ハーネス") {
		t.Errorf("MEMORY.md lost its heading:\n%s", got)
	}
}

func TestRunRejectsNameEscapingMemoryDir(t *testing.T) {
	dir := newMemoryDir(t, "", map[string]string{"keeper.md": "body"})
	outside := filepath.Join(filepath.Dir(dir), "outside.md")
	if err := os.WriteFile(outside, []byte("body"), 0o600); err != nil {
		t.Fatal(err)
	}

	err := Run(Opts{MemoryDir: dir, Names: []string{"../outside"}})
	if err == nil {
		t.Fatal("Run() = nil; want error for a name escaping the memory dir")
	}
	if !strings.Contains(err.Error(), "../outside") {
		t.Errorf("err = %v; want it to name the rejected input", err)
	}
	if _, statErr := os.Stat(outside); statErr != nil {
		t.Errorf("outside.md = %v; want left intact", statErr)
	}
}

func TestRunRefusesToDeleteTheIndexItself(t *testing.T) {
	dir := newMemoryDir(t, "- [keeper.md](keeper.md) — 残る知見\n", map[string]string{"keeper.md": "body"})

	err := Run(Opts{MemoryDir: dir, Names: []string{"MEMORY"}})
	if err == nil {
		t.Fatal("Run() = nil; want error when asked to delete the index itself")
	}
	if _, statErr := os.Stat(filepath.Join(dir, "MEMORY.md")); statErr != nil {
		t.Errorf("MEMORY.md = %v; want left intact", statErr)
	}
}

func TestRunErrorsWhenMemoryFileMissing(t *testing.T) {
	dir := newMemoryDir(t, "", map[string]string{"keeper.md": "body"})

	err := Run(Opts{MemoryDir: dir, Names: []string{"nosuch"}})
	if err == nil {
		t.Fatal("Run() = nil; want error for a missing memory file")
	}
	if !strings.Contains(err.Error(), "nosuch") {
		t.Errorf("err = %v; want it to name the missing memory", err)
	}
}

func TestRunResolvesMemoryDirFromCwdWhenNotGiven(t *testing.T) {
	home := t.TempDir()
	memDir := filepath.Join(home, ".claude", "projects", "-tmp-proj", "memory")
	if err := os.MkdirAll(memDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(memDir, "obsolete.md"), []byte("body"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := Run(Opts{Home: home, Cwd: "/tmp/proj", Names: []string{"obsolete"}}); err != nil {
		t.Fatalf("Run() = %v; want nil", err)
	}

	if _, err := os.Stat(filepath.Join(memDir, "obsolete.md")); !os.IsNotExist(err) {
		t.Errorf("obsolete.md still exists (stat err = %v); want removed", err)
	}
}

func TestRunDryRunLeavesEverythingIntact(t *testing.T) {
	index := "- [obsolete.md](obsolete.md) — 消える知見\n"
	dir := newMemoryDir(t, index, map[string]string{"obsolete.md": "body"})

	if err := Run(Opts{MemoryDir: dir, Names: []string{"obsolete"}, DryRun: true}); err != nil {
		t.Fatalf("Run() = %v; want nil", err)
	}

	if _, err := os.Stat(filepath.Join(dir, "obsolete.md")); err != nil {
		t.Errorf("obsolete.md = %v; want left intact under --dry-run", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "MEMORY.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "obsolete.md") {
		t.Errorf("MEMORY.md was rewritten under --dry-run:\n%s", got)
	}
}
