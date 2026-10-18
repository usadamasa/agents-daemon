package harvest

import "testing"

func TestDetectFeedback(t *testing.T) {
	tests := []struct {
		name     string
		in       string
		wantOK   bool
		wantKind string
	}{
		{"correction ja", "そうじゃなくて、こう書いて", true, "correction"},
		{"correction ja short", "違うよ", true, "correction"},
		{"correction en", "no, that's wrong, use X", true, "correction"},
		{"stop doing ja", "それやめて", true, "stop_doing"},
		{"stop doing en", "stop doing that", true, "stop_doing"},
		{"rejection", "その提案は却下", true, "rejection"},
		{"rejection en", "reject that approach", true, "rejection"},
		{"no signal", "ありがとう、次に進んで", false, ""},
		{"empty", "", false, ""},
		{"whitespace only", "   ", false, ""},

		// issue #173: 英語 word boundary regression - 派生形 (rejected/rejecting) は誤検出しない
		{"rejected past tense in schema", "List of rejected MCP servers from .mcp.json", false, ""},
		{"rejecting gerund", "the system is rejecting invalid input", false, ""},
		{"projection contains 'reject' substring", "project management", false, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fs, ok := detectFeedback(tc.in)
			if ok != tc.wantOK {
				t.Fatalf("detectFeedback(%q) ok=%v; want %v", tc.in, ok, tc.wantOK)
			}
			if ok && fs.Kind != tc.wantKind {
				t.Errorf("detectFeedback(%q) kind=%q; want %q", tc.in, fs.Kind, tc.wantKind)
			}
		})
	}
}

func TestDetectFeedback_QuoteMaskedAndTruncated(t *testing.T) {
	// PII を含む長文が伏せ字化 + truncate されて quote に入るか
	input := "そうじゃなくて JP-TECH-042 の件は " +
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	fs, ok := detectFeedback(input)
	if !ok {
		t.Fatal("feedback should be detected")
	}
	if len([]rune(fs.Quote)) > maxFeedbackQuote {
		t.Errorf("quote rune len = %d; want <= %d", len([]rune(fs.Quote)), maxFeedbackQuote)
	}
	if contains := indexOf(fs.Quote, "JP-TECH-042"); contains >= 0 {
		t.Errorf("quote must not contain raw candidate id: %q", fs.Quote)
	}
	if indexOf(fs.Quote, "[CANDIDATE-ID]") < 0 {
		t.Errorf("quote should contain masked marker: %q", fs.Quote)
	}
}

// indexOf は strings.Index の薄いラッパー (test 内の import 節約用)｡
func indexOf(s, substr string) int {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
