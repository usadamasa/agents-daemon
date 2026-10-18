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

	"github.com/usadamasa/agents-daemon/internal/dream/dreamstate"
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

// Run は指定された memory を削除し、MEMORY.md の索引からも除去する。
func Run(opts Opts) error {
	if opts.Stdout == nil {
		opts.Stdout = io.Discard
	}
	if opts.Stderr == nil {
		opts.Stderr = io.Discard
	}
	if len(opts.Names) == 0 {
		return errors.New("削除する memory 名を 1 つ以上指定する")
	}

	memDir, err := resolveMemoryDir(opts)
	if err != nil {
		return err
	}

	// 1 件でも解決できなければ何も消さない (部分適用で索引と実体が乖離するのを避ける)。
	targets := make([]string, 0, len(opts.Names))
	bases := make([]string, 0, len(opts.Names))
	for _, name := range opts.Names {
		path, base, err := resolveTarget(memDir, name)
		if err != nil {
			return err
		}
		if _, err := os.Stat(path); err != nil {
			return fmt.Errorf("memory %q が見つからない: %w", name, err)
		}
		targets = append(targets, path)
		bases = append(bases, base)
	}

	if opts.DryRun {
		for _, path := range targets {
			_, _ = fmt.Fprintf(opts.Stdout, "would remove %s\n", path)
		}
		return nil
	}

	for _, path := range targets {
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("削除失敗 %s: %w", path, err)
		}
		_, _ = fmt.Fprintf(opts.Stdout, "removed %s\n", path)
	}
	return pruneIndex(filepath.Join(memDir, "MEMORY.md"), bases)
}

// resolveMemoryDir は削除対象の memory ディレクトリを決める。
func resolveMemoryDir(opts Opts) (string, error) {
	if opts.MemoryDir != "" {
		return opts.MemoryDir, nil
	}
	project, err := dreamstate.ResolveProject(opts.Project, opts.Cwd)
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
