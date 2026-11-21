// Package check は dream の adopt の後に、判断の要らない点検を機械で行う。read-only で、何も直さない。
//
// auto memory の [[name]] は、指す先の memory の frontmatter の name で解く (ファイル名ではない)。
// コード (fence と inline code) の中の [[ ... ]] はリンクとして読まない。
package check

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/usadamasa/agents-daemon/internal/dream"
	"github.com/usadamasa/agents-daemon/internal/dream/mine/corpus"
)

// KindDanglingLink は auto memory の [[name]] が、どの memory の frontmatter の name にも当たらないこと。
const KindDanglingLink = "dangling_link"

// Opts は check サブコマンドの実行パラメータ。
type Opts struct {
	MemoryDir string // 空なら ~/.claude/projects/<project>/memory
	Project   string // 空なら Cwd から解決
	Cwd       string

	Home   string
	Stdout io.Writer
}

const (
	KindIndexTooLong  = "index_too_long"
	KindMemoryTooLong = "memory_too_long"

	SeverityError   = "error"
	SeverityWarning = "warning"
)

// Finding は点検に落ちた 1 件。
type Finding struct {
	Kind     string `json:"kind"`
	Severity string `json:"severity"`
	Path     string `json:"path"`
	Line     int    `json:"line"`
	Target   string `json:"target"`
}

// Result は点検の結果。stdout の行と同じものを持ち、`--json` はそのまま出す。
type Result struct {
	Findings []Finding `json:"findings"`
}

// Failed は error の項目が 1 件でもあるか。
func (r Result) Failed() bool { return false }

var (
	// linkRe は slug の形の [[name]]。shell の `[[ -f x ]]` のような空白を含むものは当たらない。
	linkRe       = regexp.MustCompile(`\[\[([A-Za-z0-9][A-Za-z0-9._-]*)\]\]`)
	inlineCodeRe = regexp.MustCompile("`[^`]*`")
)

// Run は点検を回し、落ちた項目を Stdout に 1 行ずつ書く。
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
	names := make(map[string]bool, len(mems))
	for _, m := range mems {
		if m.Name != "" {
			names[m.Name] = true
		}
	}
	for _, m := range mems {
		found, err := danglingLinks(m.Path, names)
		if err != nil {
			return res, err
		}
		res.Findings = append(res.Findings, found...)
	}
	for _, f := range res.Findings {
		_, _ = fmt.Fprintf(opts.Stdout, "%s:%d: [[%s]] の指す memory が無い\n", f.Path, f.Line, f.Target)
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

// danglingLinks は path の各行から、names に無い [[name]] を拾う。
func danglingLinks(path string, names map[string]bool) ([]Finding, error) {
	f, err := os.Open(path) // #nosec G304 -- corpus.LoadMemories が列挙した memory ディレクトリ直下の *.md
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var out []Finding
	sc := bufio.NewScanner(f)
	inFence := false
	for n := 1; sc.Scan(); n++ {
		line := sc.Text()
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		for _, m := range linkRe.FindAllStringSubmatch(inlineCodeRe.ReplaceAllString(line, ""), -1) {
			if !names[m[1]] {
				out = append(out, Finding{Kind: KindDanglingLink, Path: path, Line: n, Target: m[1]})
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("%s の読み込み失敗: %w", path, err)
	}
	return out, nil
}
