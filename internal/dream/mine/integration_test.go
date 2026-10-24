package mine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/usadamasa/agents-daemon/internal/dream/dreamio"
)

// TestIntegration_DailyStandupFixture は 5 件の feedback memory + 1 skill (daily-standup)
// + 2 harvest session の最小 fixture で、Mine パイプラインが 5 candidates を
// daily-standup にひもづけて出力することを検証する｡
//
// 具体の kind (skill_body vs memory_dedup) は日本語 paraphrasing に対する
// grep 感度に依存するため、この regression では「件数 + skill 関連付け」だけを
// 保証する｡ 分類閾値は skill-mining-heuristics.md「判定 2」を参照｡
func TestIntegration_DailyStandupFixture(t *testing.T) {
	fixDir := t.TempDir()

	// 1. harvest jsonl (workspace project セッション、daily-standup を invoke)
	harvestDir := filepath.Join(fixDir, "harvest")
	if err := os.MkdirAll(harvestDir, 0o755); err != nil {
		t.Fatal(err)
	}
	harvestContent := `{"session_id":"sess-a","cwd":"` + fixDir + `","cwd_project":"-fixture-workspace","n_messages":5,"skills_invoked":["daily-standup"]}
{"session_id":"sess-b","cwd":"` + fixDir + `","cwd_project":"-fixture-workspace","n_messages":3,"skills_invoked":["daily-standup"]}
`
	if err := os.WriteFile(filepath.Join(harvestDir, "2026-07-19.jsonl"), []byte(harvestContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// 2. memory: 5 件の feedback_standup_* (prototype 実験と同じ命名)
	memDir := filepath.Join(fixDir, "memory")
	if err := os.MkdirAll(memDir, 0o755); err != nil {
		t.Fatal(err)
	}
	memories := map[string]string{
		"feedback_standup_no_pii.md": `---
name: feedback-standup-no-pii
description: PII を伏せる
metadata:
  type: feedback
---

外部個人 (面接候補者等) の氏名・候補者 ID を伏せる｡

**Why:** コンプライアンス絶対条件｡

**How to apply:** standup 実行時に「一次面接」等に一般化する｡
`,
		"feedback_standup_default_channel.md": `---
name: feedback-standup-default-channel
description: daily-standup のデフォルト投稿先
metadata:
  type: feedback
---

デフォルトの投稿先は #z-times-usadamasa｡

**How to apply:** チャンネル指定がなければ #z-times-usadamasa に投稿する｡
`,
		"feedback_standup_name_kanji.md": `---
name: feedback-standup-name-kanji
description: 人名の漢字を推測しない
metadata:
  type: feedback
---

漢字表記が不明な人名は推測しない｡

**How to apply:** 出典のカタカナ表記をそのまま使う｡
`,
		"feedback_standup_date_range.md": `---
name: feedback-standup-date-range
description: date range
metadata:
  type: feedback
---

前営業日+当日の2日分を収集する｡Done=前営業日、Doing=当日｡

**How to apply:** activity-collector を2回呼ぶ｡
`,
		"feedback_standup_slack_filter.md": `---
name: feedback-standup-slack-filter
description: slack filter
metadata:
  type: feedback
---

Slack 投稿は明確なアクション/タスクのみ｡つぶやきは除外｡

**How to apply:** 迷ったら除外する｡
`,
	}
	for name, content := range memories {
		if err := os.WriteFile(filepath.Join(memDir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// 3. skill: daily-standup (最小 SKILL.md、references なし)
	skillsDir := filepath.Join(fixDir, "skills")
	skillDir := filepath.Join(skillsDir, "daily-standup")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	skillContent := `# daily-standup

活動を収集し Done/Doing/Blockers に分類して Slack 投稿する｡
`
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skillContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// 4. Mine 実行 (main を直接叩けないので internal 関数を組み立てる)
	sessions, err := loadHarvestForProject(harvestDir, "-fixture-workspace")
	if err != nil {
		t.Fatalf("loadHarvestForProject: %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(sessions))
	}

	skillNames := extractInvokedSkills(sessions)
	skills := make(map[string]skill)
	for _, n := range skillNames {
		s, err := loadSkill(skillsDir, n)
		if err != nil {
			continue
		}
		skills[n] = s
	}
	if _, ok := skills["daily-standup"]; !ok {
		t.Fatal("daily-standup skill not loaded")
	}

	mems, err := loadMemories(memDir)
	if err != nil {
		t.Fatalf("loadMemories: %v", err)
	}
	if len(mems) != 5 {
		t.Fatalf("expected 5 memories, got %d", len(mems))
	}

	cands := generateCandidates(mems, skills, "-fixture-workspace", sessions)

	// 5. 検証: prototype と同じ 5 候補、全て daily-standup 関連付け
	if len(cands) != 5 {
		t.Fatalf("expected 5 candidates, got %d: %+v", len(cands), cands)
	}
	seenIDs := make(map[string]struct{})
	for _, c := range cands {
		if c.AssociatedSkill != "daily-standup" {
			t.Errorf("candidate %s associated with %q; want daily-standup", c.ID, c.AssociatedSkill)
		}
		if c.ScopedToProject != "-fixture-workspace" {
			t.Errorf("candidate %s scoped to %q", c.ID, c.ScopedToProject)
		}
		if !strings.HasPrefix(c.ID, "dream-candidate-daily-standup-") {
			t.Errorf("candidate ID %q does not match expected prefix", c.ID)
		}
		if !strings.Contains(c.MemoryPath, "feedback_standup_") {
			t.Errorf("candidate %s memory path %q unexpected", c.ID, c.MemoryPath)
		}
		if len(c.EvidenceSessions) != 2 {
			t.Errorf("candidate %s evidence sessions = %d; want 2 (sess-a, sess-b)", c.ID, len(c.EvidenceSessions))
		}
		seenIDs[c.ID] = struct{}{}
	}
	if len(seenIDs) != 5 {
		t.Errorf("expected 5 unique IDs, got %d", len(seenIDs))
	}

	// 6. jsonl 出力ラウンドトリップ
	outFile := filepath.Join(fixDir, "candidates.jsonl")
	if err := dreamio.WriteCandidates(outFile, cands); err != nil {
		t.Fatal(err)
	}
	stat, err := os.Stat(outFile)
	if err != nil {
		t.Fatal(err)
	}
	if stat.Size() == 0 {
		t.Error("candidates.jsonl is empty")
	}
}
