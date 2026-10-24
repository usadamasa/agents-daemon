package mine

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

// memory は feedback / project / reference 用の auto memory 1 ファイル分の内容。
// Mine が候補判定に使うのは Type == "feedback" のみだが、他 type もロードして
// 判定側でフィルタする (skill 全般に効く rule かどうかの判断を後段に任せる)。
type memory struct {
	Path        string
	Name        string
	Description string
	Type        string // "feedback" | "project" | "reference" | ""
	Body        string
}

// loadMemories は memory ディレクトリ配下の *.md (MEMORY.md 除く) を読み込む。
// deterministic order (ファイル名 sort) で返す。
//
// ディレクトリ未存在はエラーにする。mine にとって memory は主入力なので、
// 空スライスで返すと project canonical name の解決ミスが「memory 0 件」に
// 化けて表面化しない。存在するが空のディレクトリは正常系。
func loadMemories(memDir string) ([]memory, error) {
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

	out := make([]memory, 0, len(names))
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

// parseMemoryContent は 1 ファイル分の内容から memory を組み立てる。
func parseMemoryContent(path, content string) (memory, error) {
	fm, body, err := splitFrontmatter(content)
	if err != nil {
		return memory{}, err
	}
	mem := memory{Path: path, Body: strings.TrimSpace(body)}
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

// extractApplicabilityPhrases は memory body から grep-coverage 用のフレーズを抽出する。
//
// 抽出戦略 (paraphrasing に強くする):
//  1. body を line 単位に分ける (見出し・空行は除外)
//  2. 各 line から markdown 装飾を除去
//  3. さらに日本語 punctuation ｢。 ｢｢ ｢, ｢。)｣ で分割し、sub-sentence 単位のフレーズを得る
//  4. 6 rune 未満は除外
//
// これで「前営業日 + 当日の2日分を収集する」のような自然文が
// 「前営業日 + 当日」「2日分を収集する」等の chunks に分割され、
// skill body に別表現で書かれていても部分一致しやすくなる。
func extractApplicabilityPhrases(body string) []string {
	var out []string
	seen := make(map[string]struct{})
	for raw := range strings.SplitSeq(body, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#") {
			continue
		}
		cleaned := stripMarkdown(line)
		if len([]rune(cleaned)) < 6 {
			continue
		}
		// 行そのものと、sub-sentence 分割の両方を候補に入れる
		addUnique := func(s string) {
			s = strings.TrimSpace(s)
			if len([]rune(s)) < 6 {
				return
			}
			if _, ok := seen[s]; ok {
				return
			}
			seen[s] = struct{}{}
			out = append(out, s)
		}
		addUnique(cleaned)
		for _, chunk := range splitJapaneseSentence(cleaned) {
			addUnique(chunk)
		}
	}
	return out
}

// splitJapaneseSentence は日本語文を句読点 (｢。 ｢, ｢｢) で分割し、sub-phrase の slice を返す。
// 括弧内の分割はしない (単純実装)。
func splitJapaneseSentence(s string) []string {
	// 分割文字: 全角/半角の句読点。 半角スペース連続も切る。
	sep := func(r rune) bool {
		switch r {
		case '。', '｡', '、', '､', ';', ':', '：', '；':
			return true
		}
		return false
	}
	fields := strings.FieldsFunc(s, sep)
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		f = strings.TrimSpace(f)
		if f != "" {
			out = append(out, f)
		}
	}
	return out
}

var (
	labelRe   = regexp.MustCompile(`\*\*[^*]+\*\*[::]?\s*`) // **Why:** / **How to apply:** 等
	bulletRe  = regexp.MustCompile(`^[-*]\s+`)              // 箇条書きの先頭
	prefixHdr = regexp.MustCompile(`^\d+\.\s+`)             // 番号付きリスト
)

// stripMarkdown は行内の装飾を軽く落とす (grep coverage 用)。
func stripMarkdown(line string) string {
	line = bulletRe.ReplaceAllString(line, "")
	line = prefixHdr.ReplaceAllString(line, "")
	line = labelRe.ReplaceAllString(line, "")
	// 残りの ** も装飾なので除去
	line = strings.ReplaceAll(line, "**", "")
	line = strings.TrimSpace(line)
	return line
}
