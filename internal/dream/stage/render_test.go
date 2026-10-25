package stage

import (
	"strings"
	"testing"

	"github.com/usadamasa/agents-daemon/internal/dream/mine/candidates"
)

func TestRenderReport_ContainsFrontmatterAndCandidates(t *testing.T) {
	cands := []candidates.Candidate{
		{
			ID:               "dream-candidate-daily-standup-001",
			Kind:             "skill_body",
			AssociatedSkill:  "daily-standup",
			MemoryPath:       "/mem/feedback_standup_no_pii.md",
			SkillPath:        "/skills/daily-standup/SKILL.md",
			ScopedToProject:  "-Users-x-workspace",
			Diagnosis:        "PII rule not inlined",
			EvidenceSessions: []string{"sess1", "sess2"},
			Confidence:       "high",
			CoverageRatio:    0.0,
		},
		{
			ID:               "dream-candidate-daily-standup-002",
			Kind:             "memory_dedup",
			AssociatedSkill:  "daily-standup",
			MemoryPath:       "/mem/feedback_standup_slack_filter.md",
			SkillPath:        "/skills/daily-standup/SKILL.md",
			ScopedToProject:  "-Users-x-workspace",
			Diagnosis:        "fully inlined",
			EvidenceSessions: []string{"sess1"},
			Confidence:       "high",
			CoverageRatio:    1.0,
		},
	}
	report := renderReport("-Users-x-workspace", "2026-07-20", cands)

	// explorations 用の frontmatter は付けない。読み捨てる tmp の文書なので見出しから始める。
	if !strings.HasPrefix(report, "# dream ステージレポート (2026-07-20)\n") {
		t.Errorf("report should start with the H1, got %q", strings.SplitN(report, "\n", 2)[0])
	}
	// サマリ
	if !strings.Contains(report, "候補数: 2") {
		t.Error("missing candidate count summary")
	}
	if !strings.Contains(report, "skill_body: 1") {
		t.Error("missing kind breakdown skill_body")
	}
	if !strings.Contains(report, "memory_dedup: 1") {
		t.Error("missing kind breakdown memory_dedup")
	}
	// 各 candidate section
	for _, c := range cands {
		if !strings.Contains(report, c.ID) {
			t.Errorf("candidate %s missing from report", c.ID)
		}
	}
	// 承認手順
	if !strings.Contains(report, "承認するには") {
		t.Error("missing approval instructions section")
	}
	if !strings.Contains(report, "agents-daemon dream adopt") {
		t.Error("missing adopt command hint")
	}
}

func TestRenderReport_DispositionTableLeavesUnhomedBlank(t *testing.T) {
	cands := []candidates.Candidate{
		{ID: "c-001", Kind: "skill_body", MemoryPath: "/mem/a.md"},
		{ID: "c-002", Kind: "unhomed", MemoryPath: "/mem/b.md"},
	}
	report := renderReport("-p", "2026-07-20", cands)

	// 行き先表の行だけを拾い、candidate ID → 行き先セルの対応を作る｡
	rows := map[string]string{}
	for line := range strings.SplitSeq(report, "\n") {
		if !strings.HasPrefix(line, "| c-00") {
			continue
		}
		cols := strings.Split(strings.Trim(line, "|"), "|")
		if len(cols) != 4 {
			t.Fatalf("行き先表の列数が 4 でない: %q", line)
		}
		rows[strings.TrimSpace(cols[0])] = strings.TrimSpace(cols[3])
	}
	if len(rows) != 2 {
		t.Fatalf("行き先表の行数 = %d; want 2 (全 memory が 1 行ずつ出る)", len(rows))
	}
	if rows["c-001"] == "" {
		t.Error("skill_body の行き先が空欄になっている")
	}
	if rows["c-002"] != "" {
		t.Errorf("unhomed の行き先 = %q; 人間が決めるので空欄であるべき", rows["c-002"])
	}
	if !strings.Contains(report, "unhomed: 1") {
		t.Error("内訳に unhomed の件数が無い")
	}
	if !strings.Contains(report, "行き先テーブル") {
		t.Error("unhomed があるときの案内文が無い")
	}
}

func TestRenderReport_NoUnhomedOmitsGuidance(t *testing.T) {
	cands := []candidates.Candidate{
		{ID: "c-001", Kind: "skill_body", MemoryPath: "/mem/a.md"},
	}
	report := renderReport("-p", "2026-07-20", cands)
	if strings.Contains(report, "行き先テーブル") {
		t.Error("unhomed が無いのに案内文が出ている")
	}
}

func TestRenderReport_EmptyCandidates(t *testing.T) {
	report := renderReport("-p", "2026-07-20", nil)
	if !strings.Contains(report, "候補数: 0") {
		t.Error("empty case should show 候補数: 0")
	}
	if !strings.Contains(report, "候補は見つかりませんでした") {
		t.Error("empty case should have explicit message")
	}
}

func TestProjectSlug_TrimsLeadingDash(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"-Users-x-workspace", "Users-x-workspace"},
		{"no-dash", "no-dash"},
		{"", ""},
	}
	for _, tc := range tests {
		got := projectSlug(tc.in)
		if got != tc.want {
			t.Errorf("projectSlug(%q) = %q; want %q", tc.in, got, tc.want)
		}
	}
}

func TestReportPath_UnderCwdTmp(t *testing.T) {
	got := reportPath("/work/repo", "-Users-x-workspace", "2026-07-20")
	want := "/work/repo/tmp/dream/2026-07-20-Users-x-workspace.md"
	if got != want {
		t.Errorf("reportPath = %q; want %q", got, want)
	}
}
