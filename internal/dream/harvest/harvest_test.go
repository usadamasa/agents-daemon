package harvest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeJSONLTest はテスト用に JSONL fixture を書き出す｡
func writeJSONLTest(t *testing.T, dir, name string, lines []string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	body := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// -- Fixture builders ------------------------------------------------------

// normalUserLine は最小の user entry を返す (content = string)｡
func normalUserLine(sid, ts, text string) string {
	return `{"type":"user","sessionId":"` + sid + `","cwd":"/Users/x/workspace","timestamp":"` + ts +
		`","message":{"content":` + jsonString(text) + `}}`
}

// assistantSkillLine は Skill tool_use を含む assistant entry を返す｡
func assistantSkillLine(sid, ts, skill string) string {
	return `{"type":"assistant","sessionId":"` + sid + `","timestamp":"` + ts +
		`","message":{"content":[{"type":"tool_use","name":"Skill","input":{"skill":"` + skill + `"}}]}}`
}

// jsonString は Go string を JSON string リテラルにエスケープする｡
func jsonString(s string) string {
	// 簡易版: quote/backslash だけエスケープ｡日本語はそのまま OK｡
	b := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(s)
	return `"` + b + `"`
}

// -- Tests ----------------------------------------------------------------

func TestScanSessionFile_NormalSession(t *testing.T) {
	dir := t.TempDir()
	path := writeJSONLTest(t, dir, "sess1.jsonl", []string{
		normalUserLine("sess1", "2026-07-19T10:00:00+09:00", "スタンドアップやって"),
		assistantSkillLine("sess1", "2026-07-19T10:00:05+09:00", "daily-standup"),
		normalUserLine("sess1", "2026-07-19T10:01:00+09:00", "ありがとう"),
	})

	rec, err := ScanSessionFile(path)
	if err != nil {
		t.Fatalf("ScanSessionFile: %v", err)
	}
	if rec == nil {
		t.Fatal("record should not be nil for non-empty session")
	}
	if rec.SessionID != "sess1" {
		t.Errorf("SessionID = %q; want sess1", rec.SessionID)
	}
	if rec.CWD != "/Users/x/workspace" {
		t.Errorf("CWD = %q", rec.CWD)
	}
	if rec.CWDProject != "-Users-x-workspace" {
		t.Errorf("CWDProject = %q; want -Users-x-workspace", rec.CWDProject)
	}
	if rec.NMessages != 3 {
		t.Errorf("NMessages = %d; want 3", rec.NMessages)
	}
	if len(rec.SkillsInvoked) != 1 || rec.SkillsInvoked[0] != "daily-standup" {
		t.Errorf("SkillsInvoked = %v; want [daily-standup]", rec.SkillsInvoked)
	}
	if len(rec.UserPrompts) != 2 {
		t.Errorf("UserPrompts len = %d; want 2", len(rec.UserPrompts))
	}
	if len(rec.FeedbackSignals) != 0 {
		t.Errorf("FeedbackSignals should be empty, got %v", rec.FeedbackSignals)
	}
	if len(rec.FailedInvocations) != 0 {
		t.Errorf("FailedInvocations should be empty, got %v", rec.FailedInvocations)
	}
	if rec.TSStart != "2026-07-19T10:00:00+09:00" {
		t.Errorf("TSStart = %q", rec.TSStart)
	}
	if rec.TSEnd != "2026-07-19T10:01:00+09:00" {
		t.Errorf("TSEnd = %q", rec.TSEnd)
	}
}

func TestScanSessionFile_CorrectionSession(t *testing.T) {
	dir := t.TempDir()
	path := writeJSONLTest(t, dir, "sess2.jsonl", []string{
		normalUserLine("sess2", "2026-07-19T10:00:00+09:00", "テスト書いて"),
		assistantSkillLine("sess2", "2026-07-19T10:00:05+09:00", "bats-testing"),
		normalUserLine("sess2", "2026-07-19T10:00:10+09:00", "そうじゃなくて Go のテストで書いて"),
	})

	rec, err := ScanSessionFile(path)
	if err != nil {
		t.Fatalf("ScanSessionFile: %v", err)
	}
	if len(rec.FeedbackSignals) != 1 {
		t.Fatalf("FeedbackSignals len = %d; want 1", len(rec.FeedbackSignals))
	}
	if rec.FeedbackSignals[0].Kind != "correction" {
		t.Errorf("FeedbackSignal kind = %q; want correction", rec.FeedbackSignals[0].Kind)
	}
	if len(rec.FailedInvocations) != 1 {
		t.Fatalf("FailedInvocations len = %d; want 1", len(rec.FailedInvocations))
	}
	if rec.FailedInvocations[0].InvokedSkill != "bats-testing" {
		t.Errorf("FailedInvocation.InvokedSkill = %q; want bats-testing", rec.FailedInvocations[0].InvokedSkill)
	}
}

func TestScanSessionFile_PIIMaskedInUserPrompts(t *testing.T) {
	dir := t.TempDir()
	path := writeJSONLTest(t, dir, "sess3.jsonl", []string{
		normalUserLine("sess3", "2026-07-19T10:00:00+09:00", "JP-TECH-042 の一次面接の準備"),
	})

	rec, err := ScanSessionFile(path)
	if err != nil {
		t.Fatalf("ScanSessionFile: %v", err)
	}
	if len(rec.UserPrompts) != 1 {
		t.Fatalf("UserPrompts len = %d", len(rec.UserPrompts))
	}
	if strings.Contains(rec.UserPrompts[0], "JP-TECH-042") {
		t.Errorf("raw candidate id should be masked: %q", rec.UserPrompts[0])
	}
	if !strings.Contains(rec.UserPrompts[0], "[CANDIDATE-ID]") {
		t.Errorf("should contain mask marker: %q", rec.UserPrompts[0])
	}
}

func TestScanSessionFile_MetaPromptsIgnored(t *testing.T) {
	dir := t.TempDir()
	path := writeJSONLTest(t, dir, "sess4.jsonl", []string{
		normalUserLine("sess4", "2026-07-19T10:00:00+09:00", "<command-name>test</command-name>"),
		normalUserLine("sess4", "2026-07-19T10:00:05+09:00", "<system-reminder>foo</system-reminder>"),
		normalUserLine("sess4", "2026-07-19T10:00:10+09:00", "ほんとうの質問"),
	})

	rec, err := ScanSessionFile(path)
	if err != nil {
		t.Fatalf("ScanSessionFile: %v", err)
	}
	if len(rec.UserPrompts) != 1 {
		t.Errorf("UserPrompts len = %d; want 1 (meta prompts filtered)", len(rec.UserPrompts))
	}
	if rec.UserPrompts[0] != "ほんとうの質問" {
		t.Errorf("UserPrompts[0] = %q; want %q", rec.UserPrompts[0], "ほんとうの質問")
	}
}

func TestScanSessionFile_EmptySessionReturnsNil(t *testing.T) {
	dir := t.TempDir()
	path := writeJSONLTest(t, dir, "sess5.jsonl", []string{
		// meta prompt のみ → NMessages が 0 になる
		normalUserLine("sess5", "2026-07-19T10:00:00+09:00", "<command-name>test</command-name>"),
	})

	rec, err := ScanSessionFile(path)
	if err != nil {
		t.Fatalf("ScanSessionFile: %v", err)
	}
	if rec != nil {
		t.Errorf("expected nil for empty session, got %#v", rec)
	}
}

func TestScanSessionFile_ArrayContent(t *testing.T) {
	dir := t.TempDir()
	line := `{"type":"user","sessionId":"sess6","timestamp":"2026-07-19T10:00:00+09:00","message":{"content":[{"type":"text","text":"配列コンテンツ"},{"type":"tool_result","tool_use_id":"x"}]}}`
	path := writeJSONLTest(t, dir, "sess6.jsonl", []string{line})

	rec, err := ScanSessionFile(path)
	if err != nil {
		t.Fatalf("ScanSessionFile: %v", err)
	}
	if rec == nil || len(rec.UserPrompts) != 1 || rec.UserPrompts[0] != "配列コンテンツ" {
		t.Errorf("array content extraction failed: %#v", rec)
	}
}

func TestScanSessionFile_MultipleSkillInvocations(t *testing.T) {
	dir := t.TempDir()
	path := writeJSONLTest(t, dir, "sess7.jsonl", []string{
		normalUserLine("sess7", "2026-07-19T10:00:00+09:00", "何かして"),
		assistantSkillLine("sess7", "2026-07-19T10:00:05+09:00", "skill-a"),
		assistantSkillLine("sess7", "2026-07-19T10:00:10+09:00", "skill-b"),
		assistantSkillLine("sess7", "2026-07-19T10:00:15+09:00", "skill-a"), // duplicate
	})

	rec, err := ScanSessionFile(path)
	if err != nil {
		t.Fatalf("ScanSessionFile: %v", err)
	}
	if len(rec.SkillsInvoked) != 2 {
		t.Errorf("SkillsInvoked (deduped) = %v; want 2 items", rec.SkillsInvoked)
	}
	// order preserved (first occurrence)
	if rec.SkillsInvoked[0] != "skill-a" || rec.SkillsInvoked[1] != "skill-b" {
		t.Errorf("order not preserved: %v", rec.SkillsInvoked)
	}
}

func TestScanSessionFile_UserPromptsCap(t *testing.T) {
	dir := t.TempDir()
	var lines []string
	for i := 0; i < 15; i++ {
		lines = append(lines, normalUserLine("sess8", "2026-07-19T10:00:00+09:00", "prompt"))
	}
	path := writeJSONLTest(t, dir, "sess8.jsonl", lines)

	rec, err := ScanSessionFile(path)
	if err != nil {
		t.Fatalf("ScanSessionFile: %v", err)
	}
	if len(rec.UserPrompts) != maxUserPrompts {
		t.Errorf("UserPrompts capped at %d, got %d", maxUserPrompts, len(rec.UserPrompts))
	}
	// but NMessages counts them all
	if rec.NMessages != 15 {
		t.Errorf("NMessages = %d; want 15", rec.NMessages)
	}
}

func TestScanSessionFile_UserPromptTruncated(t *testing.T) {
	long := strings.Repeat("あ", 500)
	dir := t.TempDir()
	path := writeJSONLTest(t, dir, "sess9.jsonl", []string{
		normalUserLine("sess9", "2026-07-19T10:00:00+09:00", long),
	})

	rec, err := ScanSessionFile(path)
	if err != nil {
		t.Fatalf("ScanSessionFile: %v", err)
	}
	if len([]rune(rec.UserPrompts[0])) != maxUserPromptLen {
		t.Errorf("truncated len = %d; want %d", len([]rune(rec.UserPrompts[0])), maxUserPromptLen)
	}
}

// TestIsMetaUserContent は skill body / Context ブロックが meta と判定されるか確認する｡
// これらは feedback_signals 誤検出の主要因 (issue #173) だったため、除外を保証する｡
func TestIsMetaUserContent(t *testing.T) {
	longSkillBody := "# Update Config Skill\n\n" + strings.Repeat("Modify Claude Code configuration by updating settings.json files.\n\n", 100)

	tests := []struct {
		name string
		in   string
		want bool
	}{
		// 既存挙動 (regression)
		{"command-name tag", "<command-name>test</command-name>", true},
		{"system-reminder tag", "<system-reminder>foo</system-reminder>", true},
		{"bash-input tag", "<bash-input>ls</bash-input>", true},

		// 新規除外パターン (issue #173)
		{"commit-push-pr Context block", "## Context\n\n- Current git status: On branch main\nYour branch is up to date with 'origin/main'.", true},
		{"skill Base directory header", "Base directory for this skill: /Users/x/.claude/skills/dream\n\n# dream\n\nClaude Code の記憶階層を横断して再編し...", true},
		{"long skill body (# Header)", longSkillBody, true},

		// 誤除外を避ける (真の user prompt は通す)
		{"real user rejection", "そうじゃなくて Go のテストで書いて", false},
		{"real user question with # heading", "# 私のメモ\n\n短い内容", false},
		{"real user shorter prompt", "何か作って", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := isMetaUserContent(tc.in)
			if got != tc.want {
				t.Errorf("isMetaUserContent(%q...) = %v; want %v", tc.in[:min(50, len(tc.in))], got, tc.want)
			}
		})
	}
}

// TestScanSessionFile_SkillBodyDoesNotTriggerFeedback は skill 本体テキストが user prompt として
// 展開されても FeedbackSignals に混入しないことを確認する (issue #173 regression)｡
func TestScanSessionFile_SkillBodyDoesNotTriggerFeedback(t *testing.T) {
	tests := []struct {
		name       string
		userPrompt string
	}{
		{
			name: "commit-push-pr Context with 'Do not use any other tools'",
			userPrompt: "## Context\n\n- Current git status: On branch main\n" +
				"Please output the above in a single message. Do not use any other tools or do anything else.",
		},
		{
			name: "dream skill body containing 却下",
			userPrompt: "Base directory for this skill: /Users/x/.claude/skills/dream\n\n" +
				"# dream\n\n" +
				"Session 復旧の判断: 採用したい / 却下した理由をメモに残す｡",
		},
		{
			name: "update-config skill body containing rejected MCP servers",
			userPrompt: "# Update Config Skill\n\n" + strings.Repeat("Modify Claude Code configuration.\n", 200) +
				"\"description\": \"List of rejected MCP servers from .mcp.json\"",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := writeJSONLTest(t, dir, "sess.jsonl", []string{
				normalUserLine("sess", "2026-07-20T10:00:00+09:00", tc.userPrompt),
				assistantSkillLine("sess", "2026-07-20T10:00:05+09:00", "some-skill"),
			})
			rec, err := ScanSessionFile(path)
			if err != nil {
				t.Fatalf("ScanSessionFile: %v", err)
			}
			if rec == nil {
				t.Fatal("record nil")
			}
			if len(rec.FeedbackSignals) != 0 {
				t.Errorf("FeedbackSignals must be empty for skill body content, got %d: %+v",
					len(rec.FeedbackSignals), rec.FeedbackSignals)
			}
			if len(rec.FailedInvocations) != 0 {
				t.Errorf("FailedInvocations must be empty (no real rejection), got %+v", rec.FailedInvocations)
			}
		})
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
