package check

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeMemory は memory ディレクトリに frontmatter の name を持つ memory を書く。
func writeMemory(t *testing.T, dir, file, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, file)
	content := "---\nname: " + name + "\ndescription: d\nmetadata:\n  type: feedback\n---\n\n" + body
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// 指す先の memory が無い [[name]] は、ファイルと行を添えて 1 件ずつ出る。
func TestRun_ReportsDanglingLink(t *testing.T) {
	dir := t.TempDir()
	writeMemory(t, dir, "alive.md", "alive", "本文\n")
	src := writeMemory(t, dir, "linker.md", "linker", "[[alive]] を見る。\n消えた [[gone]] も指す。\n")

	res, err := Run(Opts{MemoryDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Findings) != 1 {
		t.Fatalf("findings = %+v; want 1 件", res.Findings)
	}
	f := res.Findings[0]
	// frontmatter の 7 行と空行の後、本文の 2 行目。
	if f.Kind != KindDanglingLink || f.Path != src || f.Line != 9 || f.Target != "gone" {
		t.Errorf("finding = %+v", f)
	}
}

// リンクはファイル名ではなく frontmatter の name で解く。
func TestRun_ResolvesByFrontmatterName(t *testing.T) {
	dir := t.TempDir()
	writeMemory(t, dir, "file-base.md", "slug-name", "本文\n")
	writeMemory(t, dir, "linker.md", "linker", "[[slug-name]] と [[file-base]]\n")

	res, err := Run(Opts{MemoryDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Findings) != 1 || res.Findings[0].Target != "file-base" {
		t.Errorf("findings = %+v; want file-base だけ", res.Findings)
	}
}

// コードの中の [[ ... ]] はリンクとして読まない (shell の test 構文や例示)。
func TestRun_IgnoresCode(t *testing.T) {
	dir := t.TempDir()
	body := strings.Join([]string{
		"`[[inline]]` は例示。",
		"```sh",
		`[[ -f "$x" ]] && echo ok`,
		"[[fenced]]",
		"```",
		`if [[ -n "$y" ]]; then :; fi`,
	}, "\n") + "\n"
	writeMemory(t, dir, "code.md", "code", body)

	res, err := Run(Opts{MemoryDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Findings) != 0 {
		t.Errorf("findings = %+v; want 0 件", res.Findings)
	}
}

// 落ちる項目が無ければ Findings は空の配列 (--json で null にしない)。
func TestRun_NoFindings(t *testing.T) {
	dir := t.TempDir()
	writeMemory(t, dir, "a.md", "a", "[[b]]\n")
	writeMemory(t, dir, "b.md", "b", "[[a]]\n")

	res, err := Run(Opts{MemoryDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if res.Findings == nil || len(res.Findings) != 0 {
		t.Errorf("findings = %#v; want 空の配列", res.Findings)
	}
}

// memory ディレクトリが無いのは project の解決ミスなので、0 件ではなく error にする。
func TestRun_MissingMemoryDir(t *testing.T) {
	_, err := Run(Opts{MemoryDir: filepath.Join(t.TempDir(), "nope")})
	if err == nil {
		t.Fatal("memory ディレクトリが無いのに成功した")
	}
}

// --memory-dir が無ければ ~/.claude/projects/<project>/memory を読む。
func TestRun_ResolvesMemoryDirFromProject(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".claude", "projects", "-x-y", "memory")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	writeMemory(t, dir, "a.md", "a", "[[missing]]\n")

	res, err := Run(Opts{Project: "-x-y", Home: home})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Findings) != 1 {
		t.Errorf("findings = %+v; want 1 件", res.Findings)
	}
}
