package corpus

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"strings"
)

// WikiLink は auto memory の本文にある [[name]] の 1 箇所。Target は指す先の memory の frontmatter の name。
type WikiLink struct {
	Line   int
	Target string
}

// Line は Markdown の 1 行。N は 1 始まりの行番号。
type Line struct {
	N    int
	Text string
}

var (
	// wikiLinkRe は slug の形の [[name]]。shell の `[[ -f x ]]` のような空白を含むものは当たらない。
	wikiLinkRe   = regexp.MustCompile(`\[\[([A-Za-z0-9][A-Za-z0-9._-]*)\]\]`)
	inlineCodeRe = regexp.MustCompile("`[^`]*`")
)

// WikiLinks は path の [[name]] を行番号付きで拾う。fence と inline code の中はリンクとして読まない。
func WikiLinks(path string) ([]WikiLink, error) {
	lines, err := ProseLines(path)
	if err != nil {
		return nil, err
	}
	var out []WikiLink
	for _, l := range lines {
		for _, m := range wikiLinkRe.FindAllStringSubmatch(l.Text, -1) {
			out = append(out, WikiLink{Line: l.N, Target: m[1]})
		}
	}
	return out, nil
}

// ProseLines は path の行のうち fence の外のものを、inline code を除いて返す。
func ProseLines(path string) ([]Line, error) {
	lines, err := ReadLines(path)
	if err != nil {
		return nil, err
	}
	var out []Line
	inFence := false
	for _, l := range lines {
		trimmed := strings.TrimSpace(l.Text)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		out = append(out, Line{N: l.N, Text: inlineCodeRe.ReplaceAllString(l.Text, "")})
	}
	return out, nil
}

// ReadLines は path の全行を行番号付きで返す。
func ReadLines(path string) ([]Line, error) {
	f, err := os.Open(path) // #nosec G304 -- 呼び手が列挙した memory と skill の *.md
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var out []Line
	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		out = append(out, Line{N: n, Text: sc.Text()})
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("%s の読み込み失敗: %w", path, err)
	}
	return out, nil
}
