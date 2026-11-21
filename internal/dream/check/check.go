// Package check は dream の adopt の後に、判断の要らない点検を機械で行う。read-only で、何も直さない。
//
// 項目は error と warning に分かれ、exit code に効くのは error だけ。warning は人が判断する合図で、
// まだ無い memory を指す [[name]] (Claude Code の memory の仕様では「後で書く」の印) と、
// 細分化か剪定かを決める長さの memory がこれに当たる。
// [[name]] は指す先の memory の frontmatter の name で解く (ファイル名ではない)。
package check

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/usadamasa/agents-daemon/internal/dream"
	"github.com/usadamasa/agents-daemon/internal/dream/mine/corpus"
)

const (
	// KindDanglingLink は auto memory の [[name]] が、どの memory の frontmatter の name にも当たらないこと。
	KindDanglingLink = "dangling_link"
	// KindIndexTooLong は MEMORY.md が indexMaxLines 行以上あること。
	KindIndexTooLong = "index_too_long"
	// KindMemoryTooLong は memory 1 ファイルが memoryMaxLines 行を超えること。
	KindMemoryTooLong = "memory_too_long"

	KindMissingPath  = "missing_path"
	KindRelativeDate = "relative_date"
	KindUnknownSkill = "unknown_skill"

	SeverityError   = "error"
	SeverityWarning = "warning"
)

const (
	// indexMaxLines は MEMORY.md の行数の上限 (この行数以上で落ちる)。dream skill のチェックリストの「200 行未満」。
	indexMaxLines = 200
	// memoryMaxLines は memory 1 ファイルの行数の目安 (超えたら警告)。dream skill のトリガー条件の「100 行超」。
	memoryMaxLines = 100
)

// Opts は check サブコマンドの実行パラメータ。
type Opts struct {
	MemoryDir string // 空なら ~/.claude/projects/<project>/memory
	Project   string // 空なら Cwd から解決
	Cwd       string
	// SkillDirs は SKILL.md を点検する skill の置き場 (<dir>/<name>/SKILL.md)。
	SkillDirs []string
	// PluginsFile は Claude Code の installed_plugins.json。名前空間つきの skill 名を引く。
	PluginsFile string

	Home   string
	Stdout io.Writer
}

// Finding は点検に落ちた 1 件。Line と Target は項目によっては 0 と空。
type Finding struct {
	Kind     string `json:"kind"`
	Severity string `json:"severity"`
	Path     string `json:"path"`
	Line     int    `json:"line"`
	Target   string `json:"target"`
	Message  string `json:"message"`
}

// Result は点検の結果。stdout の行と同じものを持ち、`--json` はそのまま出す。
type Result struct {
	Findings []Finding `json:"findings"`
}

// Failed は error の項目が 1 件でもあるか。
func (r Result) Failed() bool {
	for _, f := range r.Findings {
		if f.Severity == SeverityError {
			return true
		}
	}
	return false
}

// Run は点検を回し、項目を Stdout に 1 行ずつ書く。
func Run(opts Opts) (Result, error) {
	if opts.Stdout == nil {
		opts.Stdout = io.Discard
	}
	res := Result{Findings: []Finding{}}
	memDir, err := resolveMemoryDir(opts)
	if err != nil {
		return res, err
	}
	mems, err := corpus.LoadMemories(memDir)
	if err != nil {
		return res, err
	}

	index, err := checkIndex(filepath.Join(memDir, "MEMORY.md"))
	if err != nil {
		return res, err
	}
	res.Findings = append(res.Findings, index...)

	names := make(map[string]bool, len(mems))
	for _, m := range mems {
		if m.Name != "" {
			names[m.Name] = true
		}
	}
	for _, m := range mems {
		found, err := checkMemory(m.Path, names)
		if err != nil {
			return res, err
		}
		res.Findings = append(res.Findings, found...)
	}

	for _, f := range res.Findings {
		_, _ = fmt.Fprintf(opts.Stdout, "%s:%d: %s: %s\n", f.Path, f.Line, f.Severity, f.Message)
	}
	return res, nil
}

// resolveMemoryDir は点検する memory ディレクトリを決める。
func resolveMemoryDir(opts Opts) (string, error) {
	if opts.MemoryDir != "" {
		return opts.MemoryDir, nil
	}
	project, err := dream.ResolveProject(opts.Project, opts.Cwd)
	if err != nil {
		return "", err
	}
	return filepath.Join(opts.Home, ".claude", "projects", project, "memory"), nil
}

// checkIndex は MEMORY.md の行数を見る。MEMORY.md の無い memory ディレクトリもあるので、不在は正常。
func checkIndex(path string) ([]Finding, error) {
	n, err := countLines(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if n < indexMaxLines {
		return nil, nil
	}
	return []Finding{{
		Kind: KindIndexTooLong, Severity: SeverityError, Path: path,
		Message: fmt.Sprintf("MEMORY.md が %d 行ある (%d 行未満に保つ)", n, indexMaxLines),
	}}, nil
}

// checkMemory は memory 1 ファイルの行数と、names に無い [[name]] を見る。
func checkMemory(path string, names map[string]bool) ([]Finding, error) {
	var out []Finding
	n, err := countLines(path)
	if err != nil {
		return nil, err
	}
	if n > memoryMaxLines {
		out = append(out, Finding{
			Kind: KindMemoryTooLong, Severity: SeverityWarning, Path: path,
			Message: fmt.Sprintf("%d 行ある (%d 行を超えたら細分化か剪定を決める)", n, memoryMaxLines),
		})
	}
	links, err := corpus.WikiLinks(path)
	if err != nil {
		return nil, err
	}
	for _, l := range links {
		if names[l.Target] {
			continue
		}
		out = append(out, Finding{
			Kind: KindDanglingLink, Severity: SeverityWarning, Path: path, Line: l.Line, Target: l.Target,
			Message: fmt.Sprintf("[[%s]] の指す memory が無い (後で書くつもりなら残してよい)", l.Target),
		})
	}
	return out, nil
}

// countLines はファイルの行数を数える。末尾に改行の無い最後の行も 1 行に数える。
func countLines(path string) (int, error) {
	b, err := os.ReadFile(path) // #nosec G304 -- memory ディレクトリ直下の *.md
	if err != nil {
		return 0, err
	}
	s := string(b)
	n := strings.Count(s, "\n")
	if s != "" && !strings.HasSuffix(s, "\n") {
		n++
	}
	return n, nil
}
