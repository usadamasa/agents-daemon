package mine

import "testing"

func TestComputeCoverage_FullyInline(t *testing.T) {
	skillBody := "prefix\nSlack 投稿は明確なアクション/タスクのみ\n迷ったら除外する\nsuffix\n"
	phrases := []string{
		"Slack 投稿は明確なアクション/タスクのみ",
		"迷ったら除外する",
	}
	got := computeCoverage(phrases, skillBody)
	if got.Covered != 2 || got.Total != 2 {
		t.Errorf("covered=%d total=%d", got.Covered, got.Total)
	}
	if got.ratio() != 1.0 {
		t.Errorf("ratio = %f", got.ratio())
	}
}

func TestComputeCoverage_None(t *testing.T) {
	skillBody := "totally unrelated content\n"
	phrases := []string{"面接候補者の氏名は伏せる", "候補者 ID を一般化する"}
	got := computeCoverage(phrases, skillBody)
	if got.Covered != 0 || got.Total != 2 {
		t.Errorf("covered=%d total=%d", got.Covered, got.Total)
	}
	if got.ratio() != 0.0 {
		t.Errorf("ratio = %f", got.ratio())
	}
}

func TestComputeCoverage_Partial(t *testing.T) {
	skillBody := "面接候補者の氏名は伏せる (Step 5)\n"
	phrases := []string{"面接候補者の氏名は伏せる", "候補者 ID を一般化する"}
	got := computeCoverage(phrases, skillBody)
	if got.Covered != 1 || got.Total != 2 {
		t.Errorf("covered=%d total=%d", got.Covered, got.Total)
	}
	if got.ratio() != 0.5 {
		t.Errorf("ratio = %f", got.ratio())
	}
}

func TestComputeCoverage_EmptyPhrases(t *testing.T) {
	got := computeCoverage(nil, "body")
	if got.Total != 0 || got.ratio() != 0.0 {
		t.Errorf("empty phrases: covered=%d total=%d ratio=%f", got.Covered, got.Total, got.ratio())
	}
}

func TestFindAssociatedSkill_ByName(t *testing.T) {
	skills := []string{"daily-standup", "caddi-obsidian", "weekly-report"}
	tests := []struct {
		memName string
		want    string
	}{
		{"feedback-standup-no-pii", "daily-standup"},
		{"feedback-standup-default-channel", "daily-standup"},
		{"feedback-obsidian-vault", "caddi-obsidian"},
		{"unrelated-memory", ""},
	}
	for _, tc := range tests {
		t.Run(tc.memName, func(t *testing.T) {
			got := findAssociatedSkill(tc.memName, "", skills)
			if got != tc.want {
				t.Errorf("findAssociatedSkill(%q) = %q; want %q", tc.memName, got, tc.want)
			}
		})
	}
}

// 短い skill 名が無関係な語の内部に埋もれてマッチしないことを保証する｡
// 例: skill "mo" は "mock" / "move" / "remove" の一部として hit してはいけない｡
func TestFindAssociatedSkill_ShortNameDoesNotMatchInsideWord(t *testing.T) {
	skills := []string{"mo", "bats-testing"}
	tests := []struct {
		name    string
		memName string
		memDesc string
		want    string
	}{
		{"mock は mo にマッチしない", "bats-jq-mock-aqua-shim", "", ""},
		{"move は mo にマッチしない", "feedback-file-move-grep-all-refs", "", ""},
		{"remove は mo にマッチしない", "worktree-hook-no-network", "worktree-create/remove フック内での git fetch", ""},
		{"独立した token なら mo にマッチする", "feedback-mo-preview", "", "mo"},
		{"description 中の独立した mo もマッチする", "feedback-x", "mo でプレビューする", "mo"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := findAssociatedSkill(tc.memName, tc.memDesc, skills)
			if got != tc.want {
				t.Errorf("findAssociatedSkill(%q, %q) = %q; want %q", tc.memName, tc.memDesc, got, tc.want)
			}
		})
	}
}

func TestFindAssociatedSkill_ByDescription(t *testing.T) {
	skills := []string{"daily-standup", "caddi-obsidian"}
	got := findAssociatedSkill("feedback-x", "daily-standup skill のデフォルト投稿先", skills)
	if got != "daily-standup" {
		t.Errorf("got %q", got)
	}
}

func TestClassifyCoverage_HighMigration(t *testing.T) {
	// coverage 0% → skill_body, confidence high
	c := classifyCoverage(coverage{Covered: 0, Total: 5})
	if c.Kind != "skill_body" || c.Confidence != "high" {
		t.Errorf("got %+v", c)
	}
}

func TestClassifyCoverage_FullyCovered(t *testing.T) {
	// coverage 100% → memory_dedup, confidence high
	c := classifyCoverage(coverage{Covered: 5, Total: 5})
	if c.Kind != "memory_dedup" || c.Confidence != "high" {
		t.Errorf("got %+v", c)
	}
}

func TestClassifyCoverage_MostlyCovered(t *testing.T) {
	// coverage 80% → memory_dedup, confidence medium
	c := classifyCoverage(coverage{Covered: 4, Total: 5})
	if c.Kind != "memory_dedup" || c.Confidence != "medium" {
		t.Errorf("got %+v", c)
	}
}

func TestClassifyCoverage_PartialLow(t *testing.T) {
	// coverage 20% → skill_body, confidence medium (partial but mostly missing)
	c := classifyCoverage(coverage{Covered: 1, Total: 5})
	if c.Kind != "skill_body" || c.Confidence != "medium" {
		t.Errorf("got %+v", c)
	}
}

func TestClassifyCoverage_HighBoundary15Percent(t *testing.T) {
	// coverage 15% (boundary) → skill_body, confidence high
	c := classifyCoverage(coverage{Covered: 3, Total: 20})
	if c.Kind != "skill_body" || c.Confidence != "high" {
		t.Errorf("got %+v", c)
	}
}

func TestClassifyCoverage_MediumBoundary40Percent(t *testing.T) {
	// coverage 40% (boundary) → skill_body, confidence medium
	c := classifyCoverage(coverage{Covered: 8, Total: 20})
	if c.Kind != "skill_body" || c.Confidence != "medium" {
		t.Errorf("got %+v", c)
	}
}

func TestClassifyCoverage_Middle(t *testing.T) {
	// coverage 50% → skill_body, confidence low (ambiguous)
	c := classifyCoverage(coverage{Covered: 3, Total: 6})
	if c.Kind != "skill_body" || c.Confidence != "low" {
		t.Errorf("got %+v", c)
	}
}

// paraphrasing (句読点・語順違い) を token match で吸収するテスト
func TestComputeCoverage_TokenMatchAbsorbsSimpleParaphrasing(t *testing.T) {
	// memory phrase を + / スペース / 句点 で分割すると全 token が skill に存在
	skillBody := "PII を伏せ字化する / 一般化する のが standup のルール"
	phrases := []string{"PII+伏せ字化+一般化"}
	got := computeCoverage(phrases, skillBody)
	if got.Covered != 1 {
		t.Errorf("token match should cover, covered=%d", got.Covered)
	}
}

// shingle match は語順・語尾違いに対応する
func TestComputeCoverage_ShingleMatchAbsorbsInsertion(t *testing.T) {
	// phrase と skill body で語順は同じだが、skill 側に修飾が挿入されている
	skillBody := "毎日の朝会で必ず PII は伏せ字化することを忘れないでください"
	phrases := []string{"PII は伏せ字化する"}
	got := computeCoverage(phrases, skillBody)
	if got.Covered != 1 {
		t.Errorf("shingle match should cover, covered=%d", got.Covered)
	}
}

func TestComputeCoverage_TokenMatchRejectsUnrelated(t *testing.T) {
	skillBody := "全く関係のない内容が書かれている本文"
	phrases := []string{"前営業日+当日の2日分を収集する"}
	got := computeCoverage(phrases, skillBody)
	if got.Covered != 0 {
		t.Errorf("unrelated content should not match, covered=%d", got.Covered)
	}
}

func TestTokenizeForCoverage_SplitsAndFilters(t *testing.T) {
	got := tokenizeForCoverage("前営業日+当日の2日分を収集する")
	// 期待 tokens: "前営業日", "当日の2日分を収集する" (分割 by + のみ)｡
	// 「+」で 2 分割される｡他の分割点はなし｡
	if len(got) != 2 {
		t.Errorf("got tokens %v; want 2 (split by +)", got)
	}
	if got[0] != "前営業日" || got[1] != "当日の2日分を収集する" {
		t.Errorf("got %v", got)
	}
}

func TestTokenizeForCoverage_DropsShortTokens(t *testing.T) {
	got := tokenizeForCoverage("あ+ うえお / abc")
	// "あ" (1 rune) と " " は 3 rune 未満で除外｡"うえお" と "abc" が残る｡
	if len(got) != 2 {
		t.Errorf("got %v; want 2", got)
	}
}

// テスト: strings.Contains の trimming (memory phrase の余分な空白は無視)
func TestComputeCoverage_TrimsWhitespace(t *testing.T) {
	skillBody := "PII は伏せ字化する"
	phrases := []string{"  PII は伏せ字化する  "}
	got := computeCoverage(phrases, skillBody)
	if got.Covered != 1 {
		t.Errorf("trimmed phrase should match, covered=%d", got.Covered)
	}
}
