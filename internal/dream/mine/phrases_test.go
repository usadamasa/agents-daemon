package mine

import (
	"strings"
	"testing"
)

func TestExtractApplicabilityPhrases_HowToApplyLine(t *testing.T) {
	body := `# 概要

前置き｡

**Why:** 過去に事故があった｡

**How to apply:** standup 実行時に必ず PII を伏せる｡ 迷ったら一般化する｡

## 追加ルール

- 面接候補者名は一次面接に一般化する
- ID は伏せる
`
	phrases := extractApplicabilityPhrases(body)
	// 少なくとも「PII を伏せる」「迷ったら一般化」「面接候補者名」「ID は伏せる」的なフレーズが含まれる
	joined := strings.Join(phrases, "|")
	for _, want := range []string{"standup 実行時", "PII", "一般化", "面接候補者名", "ID"} {
		if !strings.Contains(joined, want) {
			t.Errorf("phrases missing keyword %q; got: %v", want, phrases)
		}
	}
	// frontmatter や Why: の直接内容は含まれるが、markdown 装飾は削られる
	for _, unwanted := range []string{"---", "##", "**Why:**"} {
		if strings.Contains(joined, unwanted) {
			t.Errorf("phrases should not contain markdown %q; got: %v", unwanted, phrases)
		}
	}
}
