// Package forget は dream Phase 4 Prune のうち、auto memory ファイルの削除を提供する。
//
// memory ディレクトリ (~/.claude/projects/<project>/memory) は Claude Code 組み込みの
// sandbox 保護により bash の rm では消せない。`agents-daemon dream forget` を sandbox.excludedCommands に
// 登録して呼ぶことで (agents-daemon:setup skill)、削除対象を「memory ディレクトリ直下の .md」に限定した
// うえで削除する。MEMORY.md の索引行も同時に除去して索引と実体の乖離を防ぐ。
package forget

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/usadamasa/agents-daemon/internal/dream"
	"github.com/usadamasa/agents-daemon/internal/dream/mine/corpus"
)

// Opts は forget サブコマンドの実行パラメータ。
type Opts struct {
	Names     []string // memory 名 (拡張子なし)
	MemoryDir string   // 空なら ~/.claude/projects/<project>/memory
	Project   string   // 空なら Cwd から解決
	Cwd       string
	DryRun    bool

	Home   string
	Stdout io.Writer
	Stderr io.Writer
}

// Result は forget が消した (DryRun なら消すはずだった) memory のパス。stdout の行と同じものを持ち、`--json` は
// そのまま出す。
type Result struct {
	DryRun     bool     `json:"dry_run"`
	Paths      []string `json:"paths"`
	LinkedFrom []Link   `json:"linked_from"`
}

// Link は消す memory を [[name]] で指している箇所。
type Link struct {
	Path   string `json:"path"`
	Line   int    `json:"line"`
	Target string `json:"target"`
}

// Run は指定された memory を削除し、MEMORY.md の索引からも除去する。途中で削除に失敗したときは、そこまでに
// 消した分を Result に残す。
func Run(opts Opts) (Result, error) {
	if opts.Stdout == nil {
		opts.Stdout = io.Discard
	}
	if opts.Stderr == nil {
		opts.Stderr = io.Discard
	}
	res := Result{DryRun: opts.DryRun, Paths: []string{}, LinkedFrom: []Link{}}
	if len(opts.Names) == 0 {
		return res, errors.New("削除する memory 名を 1 つ以上指定する")
	}

	memDir, err := resolveMemoryDir(opts)
	if err != nil {
		return res, err
	}

	// 1 件でも解決できなければ何も消さない (部分適用で索引と実体が乖離するのを避ける)。
	targets := make([]string, 0, len(opts.Names))
	bases := make([]string, 0, len(opts.Names))
	for _, name := range opts.Names {
		path, base, err := resolveTarget(memDir, name)
		if err != nil {
			return res, err
		}
		if _, err := os.Stat(path); err != nil {
			return res, fmt.Errorf("memory %q が見つからない: %w", name, err)
		}
		targets = append(targets, path)
		bases = append(bases, base)
	}

	// 消した後に残るリンク切れを、消す前に見せる。直すかどうかは人が決めるので止めない。
	res.LinkedFrom, err = linksTo(memDir, targets)
	if err != nil {
		return res, err
	}
	for _, l := range res.LinkedFrom {
		_, _ = fmt.Fprintf(opts.Stdout, "%s:%d: [[%s]] が消す memory を指している\n", l.Path, l.Line, l.Target)
	}

	if opts.DryRun {
		for _, path := range targets {
			res.Paths = append(res.Paths, path)
			_, _ = fmt.Fprintf(opts.Stdout, "would remove %s\n", path)
		}
		return res, nil
	}

	for _, path := range targets {
		if err := os.Remove(path); err != nil {
			return res, fmt.Errorf("削除失敗 %s: %w", path, err)
		}
		res.Paths = append(res.Paths, path)
		_, _ = fmt.Fprintf(opts.Stdout, "removed %s\n", path)
	}
	return res, pruneIndex(filepath.Join(memDir, "MEMORY.md"), bases)
}

// linksTo は targets 以外の memory から、targets を [[name]] (frontmatter の name) で指す箇所を拾う。
func linksTo(memDir string, targets []string) ([]Link, error) {
	mems, err := corpus.LoadMemories(memDir)
	if err != nil {
		return nil, err
	}
	isTarget := make(map[string]bool, len(targets))
	for _, t := range targets {
		isTarget[t] = true
	}
	names := map[string]bool{}
	for _, m := range mems {
		if isTarget[m.Path] && m.Name != "" {
			names[m.Name] = true
		}
	}
	out := []Link{}
	for _, m := range mems {
		if isTarget[m.Path] {
			continue
		}
		links, err := corpus.WikiLinks(m.Path)
		if err != nil {
			return nil, err
		}
		for _, l := range links {
			if names[l.Target] {
				out = append(out, Link{Path: m.Path, Line: l.Line, Target: l.Target})
			}
		}
	}
	return out, nil
}

// resolveMemoryDir は削除対象の memory ディレクトリを決める。
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

// resolveTarget は memory 名を実ファイルパスへ解決する。
// memory ディレクトリ直下から外れる入力は拒否する。
func resolveTarget(memDir, name string) (path, base string, err error) {
	base = strings.TrimSuffix(name, ".md")
	if base == "" {
		return "", "", fmt.Errorf("memory 名 %q が空", name)
	}
	if base == "MEMORY" {
		return "", "", errors.New("MEMORY.md は索引なので削除できない")
	}
	path = filepath.Join(memDir, base+".md")
	if filepath.Dir(path) != filepath.Clean(memDir) {
		return "", "", fmt.Errorf("memory 名 %q は memory ディレクトリの外を指す", name)
	}
	return path, base, nil
}

// pruneIndex は MEMORY.md から削除済み memory を指す索引行を除去する。
// MEMORY.md が無い memory ディレクトリもあるため、不在は正常扱い。
func pruneIndex(indexPath string, bases []string) error {
	raw, err := os.ReadFile(indexPath) // #nosec G304 -- memory dir + 固定ファイル名 MEMORY.md
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("MEMORY.md 読み込み失敗: %w", err)
	}

	needles := make([]string, 0, len(bases))
	for _, base := range bases {
		needles = append(needles, "]("+base+".md)")
	}

	lines := strings.Split(string(raw), "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		if matchesAny(line, needles) {
			continue
		}
		kept = append(kept, line)
	}
	if len(kept) == len(lines) {
		return nil
	}
	// #nosec G703 -- indexPath は呼び出し元が memory dir + 固定ファイル名 MEMORY.md で組み立てる。上の ReadFile と同じ経路。
	return os.WriteFile(indexPath, []byte(strings.Join(kept, "\n")), 0o600)
}

func matchesAny(line string, needles []string) bool {
	for _, needle := range needles {
		if strings.Contains(line, needle) {
			return true
		}
	}
	return false
}
