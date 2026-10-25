package corpus

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Memory は feedback / project / reference 用の auto memory 1 ファイル分の内容。
// Mine が候補判定に使うのは Type == "feedback" のみだが、他 type もロードして
// 判定側でフィルタする (skill 全般に効く rule かどうかの判断を後段に任せる)。
type Memory struct {
	Path        string
	Name        string
	Description string
	Type        string // "feedback" | "project" | "reference" | ""
	Body        string
}

// LoadMemories は memory ディレクトリ配下の *.md (MEMORY.md 除く) を読み込む。
// deterministic order (ファイル名 sort) で返す。
//
// ディレクトリ未存在はエラーにする。mine にとって memory は主入力なので、
// 空スライスで返すと project canonical name の解決ミスが「memory 0 件」に
// 化けて表面化しない。存在するが空のディレクトリは正常系。
func LoadMemories(memDir string) ([]Memory, error) {
	entries, err := os.ReadDir(memDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("memory ディレクトリが存在しない: %s", memDir)
		}
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		n := e.Name()
		if !strings.HasSuffix(n, ".md") {
			continue
		}
		if n == "MEMORY.md" {
			continue
		}
		names = append(names, n)
	}
	sort.Strings(names)

	out := make([]Memory, 0, len(names))
	for _, n := range names {
		path := filepath.Join(memDir, n)
		data, err := os.ReadFile(path) // #nosec G304 -- CLI: memory dir 配下の *.md 限定
		if err != nil {
			return nil, err
		}
		mem, err := parseMemoryContent(path, string(data))
		if err != nil {
			// skip invalid frontmatter files but continue
			continue
		}
		out = append(out, mem)
	}
	return out, nil
}

var (
	// frontmatter は "---\n" ... "\n---\n" を切り出す。
	frontmatterRe = regexp.MustCompile(`(?s)\A---\n(.*?)\n---\n?`)
	// 単純な YAML key: value 抽出 (ネストは metadata.type だけ扱う)。
	yamlLineRe = regexp.MustCompile(`(?m)^\s*([a-zA-Z_][\w-]*)\s*:\s*(.*?)\s*$`)
	yamlTypeRe = regexp.MustCompile(`(?m)^(?:\s+type\s*:\s*|\s*type\s*:\s*)(.+?)\s*$`)
)

// splitFrontmatter は content を frontmatter 部分と body 部分に分ける。
// frontmatter が無い場合は fm="" body=content。
func splitFrontmatter(content string) (string, string, error) {
	loc := frontmatterRe.FindStringSubmatchIndex(content)
	if loc == nil {
		return "", content, nil
	}
	fm := content[loc[2]:loc[3]]
	body := content[loc[1]:]
	return fm, body, nil
}

// parseMemoryContent は 1 ファイル分の内容から Memory を組み立てる。
func parseMemoryContent(path, content string) (Memory, error) {
	fm, body, err := splitFrontmatter(content)
	if err != nil {
		return Memory{}, err
	}
	mem := Memory{Path: path, Body: strings.TrimSpace(body)}
	// 単純な YAML パーサ (top-level key と metadata.type だけ) 。
	// PyYAML 等の依存を避けるため regexp ベース。
	for _, m := range yamlLineRe.FindAllStringSubmatch(fm, -1) {
		key := m[1]
		val := strings.TrimSpace(m[2])
		val = strings.TrimPrefix(val, `"`)
		val = strings.TrimSuffix(val, `"`)
		switch key {
		case "name":
			mem.Name = val
		case "description":
			mem.Description = val
		case "type":
			// top-level `type: feedback` (metadata.type ネストが無い形式)
			if mem.Type == "" {
				mem.Type = val
			}
		}
	}
	// metadata.type: ネスト形式を検出
	for _, m := range yamlTypeRe.FindAllStringSubmatch(fm, -1) {
		val := strings.TrimSpace(m[1])
		val = strings.TrimPrefix(val, `"`)
		val = strings.TrimSuffix(val, `"`)
		if val != "" {
			mem.Type = val
		}
	}
	return mem, nil
}
