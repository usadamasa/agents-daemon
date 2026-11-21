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

	res, err := Run(Opts{MemoryDir: dir, Names: []string{"obsolete"}})
	if err != nil {
		t.Fatalf("Run() = %v; want nil", err)
	}
	// 返り値は stdout の removed 行と同じパス (--json が出す)。
	if res.DryRun || len(res.Paths) != 1 || res.Paths[0] != filepath.Join(dir, "obsolete.md") {
		t.Errorf("result = %+v; want obsolete.md を消した 1 件", res)
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

	if _, err := Run(Opts{MemoryDir: dir, Names: []string{"obsolete"}}); err != nil {
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

	_, err := Run(Opts{MemoryDir: dir, Names: []string{"../outside"}})
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

	_, err := Run(Opts{MemoryDir: dir, Names: []string{"MEMORY"}})
	if err == nil {
		t.Fatal("Run() = nil; want error when asked to delete the index itself")
	}
	if _, statErr := os.Stat(filepath.Join(dir, "MEMORY.md")); statErr != nil {
		t.Errorf("MEMORY.md = %v; want left intact", statErr)
	}
}

func TestRunErrorsWhenMemoryFileMissing(t *testing.T) {
	dir := newMemoryDir(t, "", map[string]string{"keeper.md": "body"})

	_, err := Run(Opts{MemoryDir: dir, Names: []string{"nosuch"}})
	if err == nil {
		t.Fatal("Run() = nil; want error for a missing memory file")
	}
	if !strings.Contains(err.Error(), "nosuch") {
		t.Errorf("err = %v; want it to name the missing memory", err)
	}
}

func TestRunResolvesMemoryDirFromCwdWhenNotGiven(t *testing.T) {
	claudeDir := t.TempDir()
	memDir := filepath.Join(claudeDir, "projects", "-tmp-proj", "memory")
	if err := os.MkdirAll(memDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(memDir, "obsolete.md"), []byte("body"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := Run(Opts{ClaudeDir: claudeDir, Cwd: "/tmp/proj", Names: []string{"obsolete"}}); err != nil {
		t.Fatalf("Run() = %v; want nil", err)
	}

	if _, err := os.Stat(filepath.Join(memDir, "obsolete.md")); !os.IsNotExist(err) {
		t.Errorf("obsolete.md still exists (stat err = %v); want removed", err)
	}
}

// 消す memory を [[name]] で指している memory を、消す前に列挙する。止めはしない (指す側を直すかは人が決める)。
// [[name]] は消す memory の frontmatter の name で照らす。
func TestRunListsMemoriesLinkingToTheTarget(t *testing.T) {
	dir := newMemoryDir(t, "", map[string]string{
		"obsolete.md": "---\nname: obsolete-slug\n---\n\nbody\n",
		"linker.md":   "---\nname: linker\n---\n\n関連は [[obsolete-slug]]。\n`[[obsolete-slug]]` は例示。\n",
		"other.md":    "---\nname: other\n---\n\n[[linker]]\n",
	})

	var stdout strings.Builder
	res, err := Run(Opts{MemoryDir: dir, Names: []string{"obsolete"}, DryRun: true, Stdout: &stdout})
	if err != nil {
		t.Fatalf("Run() = %v; want nil", err)
	}
	linker := filepath.Join(dir, "linker.md")
	if len(res.LinkedFrom) != 1 || res.LinkedFrom[0].Path != linker || res.LinkedFrom[0].Line != 5 ||
		res.LinkedFrom[0].Target != "obsolete-slug" {
		t.Errorf("linked_from = %+v; want linker.md:5 の 1 件", res.LinkedFrom)
	}
	if !strings.Contains(stdout.String(), linker+":5:") {
		t.Errorf("stdout にリンク元が無い: %q", stdout.String())
	}

	// 消すのは止めない。
	if _, err := Run(Opts{MemoryDir: dir, Names: []string{"obsolete"}}); err != nil {
		t.Fatalf("Run() = %v; want nil", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "obsolete.md")); !os.IsNotExist(err) {
		t.Errorf("obsolete.md still exists (stat err = %v); want removed", err)
	}
}

// 消す memory 同士のリンクは、どちらも消えるので列挙しない。
func TestRunIgnoresLinksAmongTargets(t *testing.T) {
	dir := newMemoryDir(t, "", map[string]string{
		"a.md": "---\nname: a\n---\n\n[[b]]\n",
		"b.md": "---\nname: b\n---\n\n[[a]]\n",
	})
	res, err := Run(Opts{MemoryDir: dir, Names: []string{"a", "b"}, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.LinkedFrom) != 0 {
		t.Errorf("linked_from = %+v; want 0 件", res.LinkedFrom)
	}
}

func TestRunDryRunLeavesEverythingIntact(t *testing.T) {
	index := "- [obsolete.md](obsolete.md) — 消える知見\n"
	dir := newMemoryDir(t, index, map[string]string{"obsolete.md": "body"})

	res, err := Run(Opts{MemoryDir: dir, Names: []string{"obsolete"}, DryRun: true})
	if err != nil {
		t.Fatalf("Run() = %v; want nil", err)
	}
	if !res.DryRun || len(res.Paths) != 1 {
		t.Errorf("result = %+v; want dry_run で 1 件", res)
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
