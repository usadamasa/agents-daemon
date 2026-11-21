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

var (
	// wikiLinkRe は slug の形の [[name]]。shell の `[[ -f x ]]` のような空白を含むものは当たらない。
	wikiLinkRe   = regexp.MustCompile(`\[\[([A-Za-z0-9][A-Za-z0-9._-]*)\]\]`)
	inlineCodeRe = regexp.MustCompile("`[^`]*`")
)

// WikiLinks は path の [[name]] を行番号付きで拾う。fence と inline code の中はリンクとして読まない。
func WikiLinks(path string) ([]WikiLink, error) {
	f, err := os.Open(path) // #nosec G304 -- 呼び手が列挙した memory ディレクトリ直下の *.md
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var out []WikiLink
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
		for _, m := range wikiLinkRe.FindAllStringSubmatch(inlineCodeRe.ReplaceAllString(line, ""), -1) {
			out = append(out, WikiLink{Line: n, Target: m[1]})
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("%s の読み込み失敗: %w", path, err)
	}
	return out, nil
}
