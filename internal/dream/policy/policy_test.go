package policy

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestPathsFromConfig_NoFileMeansNoPolicy(t *testing.T) {
	paths, err := pathsFromConfig(filepath.Join(t.TempDir(), "config.json"), t.TempDir())
	if err != nil || paths != nil {
		t.Fatalf("got (%v, %v); want (nil, nil)", paths, err)
	}
}

func TestPathsFromConfig_NoKeyMeansNoPolicy(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "config.json")
	writeFile(t, cfg, `{"pollIntervalSeconds": 5}`)
	paths, err := pathsFromConfig(cfg, t.TempDir())
	if err != nil || paths != nil {
		t.Fatalf("got (%v, %v); want (nil, nil)", paths, err)
	}
}

func TestPathsFromConfig_ResolvesHomeAndRelative(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	cfg := filepath.Join(dir, "config.json")
	writeFile(t, cfg, `{"dream": {"policy": ["~/a.json", "b.json", "/abs/c.json"]}}`)
	paths, err := pathsFromConfig(cfg, home)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{filepath.Join(home, "a.json"), filepath.Join(dir, "b.json"), "/abs/c.json"}
	if !reflect.DeepEqual(paths, want) {
		t.Errorf("got %v; want %v", paths, want)
	}
}

// キーがあるのに読めない設定は、規約なしで黙って動かずエラーにする (PII の除外が外れたまま harvest しない)。
func TestPathsFromConfig_RejectsMalformed(t *testing.T) {
	for name, body := range map[string]string{
		"broken json":     `{"dream": `,
		"dream not obj":   `{"dream": "x"}`,
		"policy string":   `{"dream": {"policy": "a.json"}}`,
		"policy empty el": `{"dream": {"policy": [""]}}`,
		"unknown key":     `{"dream": {"polcy": ["a.json"]}}`,
	} {
		t.Run(name, func(t *testing.T) {
			cfg := filepath.Join(t.TempDir(), "config.json")
			writeFile(t, cfg, body)
			if _, err := pathsFromConfig(cfg, t.TempDir()); err == nil {
				t.Errorf("want error for %s", body)
			}
		})
	}
}

func TestLoad_NoPathsIsEmptyPolicy(t *testing.T) {
	p, err := load(nil, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(p, Policy{}) {
		t.Errorf("got %+v; want empty", p)
	}
}

func TestLoad_MergesFilesInOrder(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	a := filepath.Join(dir, "personal", "policy.json")
	b := filepath.Join(dir, "work", "policy.json")
	writeFile(t, a, `{
		"version": 1,
		"pii": [{"name": "member-id", "pattern": "\\bMEM-\\d{4}\\b", "replacement": "[MEMBER-ID]"}],
		"skillNamePrefixes": ["daily"],
		"skillDirs": ["skills", "~/more-skills"],
		"delegates": [{"topic": "settings と hook", "skill": "example:config"}]
	}`)
	writeFile(t, b, `{
		"version": 1,
		"pii": [{"name": "ticket", "pattern": "\\bTKT-\\d+\\b", "replacement": "[TICKET]"}],
		"skillNamePrefixes": ["team"],
		"skillGuides": [{"scope": "仕事用の skill", "skill": "example:skill-guide"}],
		"memoryHomes": [{"when": "社外に出せない情報を含む", "home": "private リポジトリの skill"}]
	}`)
	p, err := load([]string{a, b}, home)
	if err != nil {
		t.Fatal(err)
	}
	want := Policy{
		Sources: []string{a, b},
		PII: []PIIRule{
			{Name: "member-id", Pattern: `\bMEM-\d{4}\b`, Replacement: "[MEMBER-ID]"},
			{Name: "ticket", Pattern: `\bTKT-\d+\b`, Replacement: "[TICKET]"},
		},
		SkillNamePrefixes: []string{"daily", "team"},
		SkillDirs:         []string{filepath.Join(dir, "personal", "skills"), filepath.Join(home, "more-skills")},
		Delegates:         []Delegate{{Topic: "settings と hook", Skill: "example:config"}},
		SkillGuides:       []SkillGuide{{Scope: "仕事用の skill", Skill: "example:skill-guide"}},
		MemoryHomes:       []MemoryHome{{When: "社外に出せない情報を含む", Home: "private リポジトリの skill"}},
	}
	if !reflect.DeepEqual(p, want) {
		t.Errorf("got  %+v\nwant %+v", p, want)
	}
}

// 規約が 1 つでも読めなければ全体をエラーにする。読めた分だけで動くと、仕事用の規約の PII 規則が黙って外れる。
func TestLoad_RejectsInvalidFile(t *testing.T) {
	for name, body := range map[string]string{
		"missing version":   `{"pii": []}`,
		"wrong version":     `{"version": 2}`,
		"unknown field":     `{"version": 1, "pi": []}`,
		"bad regexp":        `{"version": 1, "pii": [{"name": "x", "pattern": "(", "replacement": "[X]"}]}`,
		"empty pattern":     `{"version": 1, "pii": [{"name": "x", "pattern": "", "replacement": "[X]"}]}`,
		"empty replacement": `{"version": 1, "pii": [{"name": "x", "pattern": "a", "replacement": ""}]}`,
		"empty name":        `{"version": 1, "pii": [{"name": "", "pattern": "a", "replacement": "[X]"}]}`,
		"empty prefix":      `{"version": 1, "skillNamePrefixes": [""]}`,
		"empty skill dir":   `{"version": 1, "skillDirs": [""]}`,
		"empty delegate":    `{"version": 1, "delegates": [{"topic": "x", "skill": ""}]}`,
		"empty guide":       `{"version": 1, "skillGuides": [{"scope": "", "skill": "x"}]}`,
		"empty home":        `{"version": 1, "memoryHomes": [{"when": "x", "home": ""}]}`,
		"broken json":       `{"version": 1,`,
		"trailing data":     `{"version": 1} {"version": 1}`,
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			good := filepath.Join(dir, "good.json")
			bad := filepath.Join(dir, "bad.json")
			writeFile(t, good, `{"version": 1}`)
			writeFile(t, bad, body)
			_, err := load([]string{good, bad}, t.TempDir())
			if err == nil {
				t.Fatalf("want error for %s", body)
			}
			if !strings.Contains(err.Error(), bad) {
				t.Errorf("error should name the file: %v", err)
			}
		})
	}
}

func TestLoad_MissingFileIsError(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope.json")
	if _, err := load([]string{missing}, t.TempDir()); err == nil {
		t.Fatal("want error for missing file")
	}
}

func TestFromConfig_NoKeyIsEmpty(t *testing.T) {
	p, err := FromConfig(filepath.Join(t.TempDir(), "config.json"), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(p, Policy{}) {
		t.Errorf("got %+v; want empty", p)
	}
}

func TestFromConfig_LoadsListedFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "p.json"), `{"version": 1, "skillNamePrefixes": ["team"]}`)
	cfg := filepath.Join(dir, "config.json")
	writeFile(t, cfg, `{"dream": {"policy": ["p.json"]}}`)
	p, err := FromConfig(cfg, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(p.SkillNamePrefixes, []string{"team"}) {
		t.Errorf("got %+v", p)
	}
}
