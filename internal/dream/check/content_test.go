package check

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// writeFile は親ディレクトリごと path にファイルを書く。
func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// findingsOf は kind の項目だけを抜き出す。
func findingsOf(res Result, kind string) []Finding {
	var out []Finding
	for _, f := range res.Findings {
		if f.Kind == kind {
			out = append(out, f)
		}
	}
	return out
}

// SKILL.md が参照する references/・scripts/・prompts/ のパスは、skill のディレクトリから見て実在する。
// plugin root からのパス (${CLAUDE_PLUGIN_ROOT}/scripts/...) は skill のディレクトリから解けないので見ない。
func TestRun_SkillRelativePaths(t *testing.T) {
	skills := t.TempDir()
	skillMd := filepath.Join(skills, "demo", "SKILL.md")
	writeFile(t, filepath.Join(skills, "demo", "references", "here.md"), "x\n")
	writeFile(t, filepath.Join(skills, "demo", "scripts", "run.sh"), "x\n")
	writeFile(t, skillMd, "---\nname: demo\n---\n\n"+
		"[here](references/here.md) と `scripts/run.sh` を使う。\n"+
		"消えた [gone](references/gone.md)。\n"+
		"`\"${CLAUDE_PLUGIN_ROOT}/scripts/elsewhere.sh\"` は plugin root から。\n"+
		"prompts/missing.md も読む。\n")

	res, err := Run(Opts{MemoryDir: t.TempDir(), SkillDirs: []string{skills}})
	if err != nil {
		t.Fatal(err)
	}
	got := findingsOf(res, KindMissingPath)
	if len(got) != 2 {
		t.Fatalf("findings = %+v; want gone.md と missing.md の 2 件", res.Findings)
	}
	if got[0].Path != skillMd || got[0].Line != 6 || got[0].Target != "references/gone.md" || got[0].Severity != SeverityError {
		t.Errorf("finding[0] = %+v", got[0])
	}
	if got[1].Line != 8 || got[1].Target != "prompts/missing.md" {
		t.Errorf("finding[1] = %+v", got[1])
	}
	if !res.Failed() {
		t.Error("参照先が無いのに Failed() が偽")
	}
}

// Markdown のリンクの表示文字列は参照ではないので、中のパスは見ない (指す先は括弧の中)。
func TestRun_SkillPathInLinkTextIsIgnored(t *testing.T) {
	skills := t.TempDir()
	writeFile(t, filepath.Join(skills, "other", "references", "rules.md"), "x\n")
	writeFile(t, filepath.Join(skills, "demo", "SKILL.md"),
		"[other の references/rules.md](../other/references/rules.md) を見る。\n")

	res, err := Run(Opts{MemoryDir: t.TempDir(), SkillDirs: []string{skills}})
	if err != nil {
		t.Fatal(err)
	}
	if got := findingsOf(res, KindMissingPath); len(got) != 0 {
		t.Errorf("findings = %+v; want 0 件", got)
	}
}

// skill のディレクトリが無いのは、global skill を置いていない環境なので点検を飛ばす。
func TestRun_MissingSkillDirIsSkipped(t *testing.T) {
	res, err := Run(Opts{MemoryDir: t.TempDir(), SkillDirs: []string{filepath.Join(t.TempDir(), "nope")}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Findings) != 0 {
		t.Errorf("findings = %+v; want 0 件", res.Findings)
	}
}

// auto memory (本体と MEMORY.md) の相対日付は、未来のセッションで解けないので落とす。コードの中は見ない。
func TestRun_RelativeDates(t *testing.T) {
	dir := t.TempDir()
	mem := writeMemory(t, dir, "a.md", "a", "先週から挙動が変わった。\n`今月` は例示。\n2026-11-01 に変わった。\n")
	index := filepath.Join(dir, "MEMORY.md")
	writeFile(t, index, "- [a](a.md) — 昨日の知見\n")

	res, err := Run(Opts{MemoryDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	got := findingsOf(res, KindRelativeDate)
	if len(got) != 2 {
		t.Fatalf("findings = %+v; want 先週 と 昨日 の 2 件", res.Findings)
	}
	byTarget := map[string]Finding{}
	for _, f := range got {
		byTarget[f.Target] = f
	}
	if f := byTarget["先週"]; f.Path != mem || f.Line != 8 || f.Severity != SeverityError {
		t.Errorf("先週 = %+v", f)
	}
	if f := byTarget["昨日"]; f.Path != index || f.Line != 1 {
		t.Errorf("昨日 = %+v", f)
	}
}

// writePlugins は installed_plugins.json と、その plugin の skill と command を置く。
func writePlugins(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	install := filepath.Join(root, "cache", "mkt", "tools", "1.0.0")
	writeFile(t, filepath.Join(install, "skills", "real-skill", "SKILL.md"), "x\n")
	writeFile(t, filepath.Join(install, "commands", "real-command.md"), "x\n")
	body, err := json.Marshal(map[string]any{
		"version": 2,
		"plugins": map[string]any{"tools@mkt": []map[string]any{{"installPath": install}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "installed_plugins.json")
	writeFile(t, file, string(body))
	return file
}

// inline code の <plugin>:<name> は、plugin が入っていればその skill か command が実在する。
// 入っていない plugin と、名前空間の無い名前は判定できないので見ない。
func TestRun_UnknownSkillReferences(t *testing.T) {
	plugins := writePlugins(t)
	dir := t.TempDir()
	mem := writeMemory(t, dir, "a.md", "a",
		"`tools:real-skill` と `/tools:real-command` は在る。\n"+
			"`tools:gone-skill` は無い。\n"+
			"`other:anything` は判定できない。`plain-skill` も見ない。\n")
	skills := t.TempDir()
	skillMd := filepath.Join(skills, "demo", "SKILL.md")
	writeFile(t, skillMd, "`/tools:gone-command` を打つ。\n")

	res, err := Run(Opts{MemoryDir: dir, SkillDirs: []string{skills}, PluginsFile: plugins})
	if err != nil {
		t.Fatal(err)
	}
	got := findingsOf(res, KindUnknownSkill)
	if len(got) != 2 {
		t.Fatalf("findings = %+v; want gone-skill と gone-command の 2 件", res.Findings)
	}
	byTarget := map[string]Finding{}
	for _, f := range got {
		byTarget[f.Target] = f
	}
	if f := byTarget["tools:gone-skill"]; f.Path != mem || f.Line != 9 || f.Severity != SeverityError {
		t.Errorf("tools:gone-skill = %+v", f)
	}
	if f := byTarget["tools:gone-command"]; f.Path != skillMd || f.Line != 1 {
		t.Errorf("tools:gone-command = %+v", f)
	}
}

// installed_plugins.json が無いのは plugin を入れていない環境なので、名前空間つきの名前は見ない。
func TestRun_MissingPluginsFile(t *testing.T) {
	dir := t.TempDir()
	writeMemory(t, dir, "a.md", "a", "`tools:gone-skill`\n")
	res, err := Run(Opts{MemoryDir: dir, PluginsFile: filepath.Join(t.TempDir(), "nope.json")})
	if err != nil {
		t.Fatal(err)
	}
	if len(findingsOf(res, KindUnknownSkill)) != 0 {
		t.Errorf("findings = %+v; want 0 件", res.Findings)
	}
}
