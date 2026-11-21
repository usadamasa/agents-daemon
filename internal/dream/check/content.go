package check

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/usadamasa/agents-daemon/internal/dream/mine/corpus"
)

var (
	// skillPathRe は skill のディレクトリからの相対パス。直前が語・`/`・`}` のもの
	// (${CLAUDE_PLUGIN_ROOT}/scripts/... や foo/references/...) は skill のディレクトリから解けないので当てない。
	skillPathRe = regexp.MustCompile("(?:^|[\\s(\\[`\"'])((?:references|scripts|prompts)/[A-Za-z0-9._/-]+)")
	// parentLinkRe は Markdown のリンクの指す先のうち `../` で始まるもの (別の skill、repo 直下)。anchor は含めない。
	// inline code の `../...` は cwd からのパスを打つコマンドと区別できないので当てない。
	parentLinkRe = regexp.MustCompile(`\]\((\.\./[^)\s#]+)`)
	// linkTextRe は Markdown のリンクの表示文字列。参照ではないので、括弧の中の指す先だけを残す。
	linkTextRe = regexp.MustCompile(`\[[^\]]*\]\(`)
	// relativeDateRe は未来のセッションで解けない日付の語。
	relativeDateRe = regexp.MustCompile(`一昨日|昨日|明日|先週|今週|来週|先月|今月|来月|去年|昨年|今年`)
	// skillRefRe は inline code の <plugin>:<name> (スラッシュコマンドの / は付いてもよい)。
	skillRefRe = regexp.MustCompile("`/?([a-z0-9][a-z0-9-]*):([a-z0-9][a-z0-9-]*)`")
)

// skillFiles は dirs の <name>/SKILL.md を列挙する。無い dir は global skill を置いていない環境なので飛ばす。
func skillFiles(dirs []string) ([]string, error) {
	var out []string
	for _, d := range dirs {
		entries, err := os.ReadDir(d)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			path := filepath.Join(d, e.Name(), "SKILL.md")
			if _, err := os.Stat(path); err == nil {
				out = append(out, path)
			}
		}
	}
	return out, nil
}

// checkSkillPaths は SKILL.md が参照する references/・scripts/・prompts/ のパスと、`../` で上がるリンクの指す先の
// 実在を見る。起点はどちらも skill のディレクトリで、symlink なら実体から解く。
// 「X skill の `references/...`」のような前置きは読まない (文章から置き場を推測すると、当て損ねが見逃しになる)。
func checkSkillPaths(skillMd string) ([]Finding, error) {
	dir, err := filepath.EvalSymlinks(filepath.Dir(skillMd))
	if err != nil {
		return nil, err
	}
	lines, err := corpus.ReadLines(skillMd)
	if err != nil {
		return nil, err
	}
	var out []Finding
	for _, l := range lines {
		for _, m := range skillPathRe.FindAllStringSubmatch(linkTextRe.ReplaceAllString(l.Text, "("), -1) {
			rel := strings.TrimRight(m[1], ".")
			if _, err := os.Stat(filepath.Join(dir, rel)); err == nil {
				continue
			}
			out = append(out, Finding{
				Kind: KindMissingPath, Severity: SeverityError, Path: skillMd, Line: l.N, Target: rel,
				Message: fmt.Sprintf("%s が skill のディレクトリに無い (別の skill や repo 直下は ../ で上がるリンクで書く)", rel),
			})
		}
		for _, m := range parentLinkRe.FindAllStringSubmatch(l.Text, -1) {
			if _, err := os.Stat(filepath.Join(dir, m[1])); err == nil {
				continue
			}
			out = append(out, Finding{
				Kind: KindMissingPath, Severity: SeverityError, Path: skillMd, Line: l.N, Target: m[1],
				Message: fmt.Sprintf("リンクの指す %s が skill のディレクトリから解けない", m[1]),
			})
		}
	}
	return out, nil
}

// checkRelativeDates は path の相対日付を拾う。コードの中は見ない。
func checkRelativeDates(path string) ([]Finding, error) {
	lines, err := corpus.ProseLines(path)
	if err != nil {
		return nil, err
	}
	var out []Finding
	for _, l := range lines {
		for _, w := range relativeDateRe.FindAllString(l.Text, -1) {
			out = append(out, Finding{
				Kind: KindRelativeDate, Severity: SeverityError, Path: path, Line: l.N, Target: w,
				Message: fmt.Sprintf("相対日付「%s」が残っている (削るか YYYY-MM-DD へ直す)", w),
			})
		}
	}
	return out, nil
}

// plugins は入っている plugin の名前から、install 先のディレクトリを引く。
type plugins map[string][]string

// loadPlugins は installed_plugins.json を読む。無いのは plugin を入れていない環境なので空。
// cc-premise: 2.1.330 plugins plugins.<name>@<marketplace>[].installPath の下に skills/<name>/SKILL.md と commands/<name>.md
func loadPlugins(path string) (plugins, error) {
	out := plugins{}
	if path == "" {
		return out, nil
	}
	b, err := os.ReadFile(path) // #nosec G304 -- Claude Code の installed_plugins.json
	if errors.Is(err, fs.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	var doc struct {
		Plugins map[string][]struct {
			InstallPath string `json:"installPath"`
		} `json:"plugins"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("%s を読めない: %w", path, err)
	}
	for key, entries := range doc.Plugins {
		name, _, _ := strings.Cut(key, "@")
		for _, e := range entries {
			out[name] = append(out[name], e.InstallPath)
		}
	}
	return out, nil
}

// has は plugin に name の skill か command があるか。plugin が入っていなければ判定できないので ok を偽で返す。
func (p plugins) has(plugin, name string) (found, ok bool) {
	paths, ok := p[plugin]
	if !ok {
		return false, false
	}
	for _, root := range paths {
		for _, c := range []string{
			filepath.Join(root, "skills", name, "SKILL.md"),
			filepath.Join(root, "commands", name+".md"),
		} {
			if _, err := os.Stat(c); err == nil {
				return true, true
			}
		}
	}
	return false, true
}

// checkSkillRefs は path の inline code にある <plugin>:<name> のうち、入っている plugin に無いものを拾う。
func checkSkillRefs(path string, p plugins) ([]Finding, error) {
	if len(p) == 0 {
		return nil, nil
	}
	lines, err := corpus.ReadLines(path)
	if err != nil {
		return nil, err
	}
	var out []Finding
	for _, l := range lines {
		for _, m := range skillRefRe.FindAllStringSubmatch(l.Text, -1) {
			if found, ok := p.has(m[1], m[2]); found || !ok {
				continue
			}
			ref := m[1] + ":" + m[2]
			out = append(out, Finding{
				Kind: KindUnknownSkill, Severity: SeverityError, Path: path, Line: l.N, Target: ref,
				Message: fmt.Sprintf("%s が plugin %s の skill にも command にも無い", ref, m[1]),
			})
		}
	}
	return out, nil
}
