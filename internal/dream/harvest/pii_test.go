package harvest

import (
	"testing"

	"github.com/usadamasa/agents-daemon/internal/dream/policy"
)

func mustMasker(t *testing.T, rules []policy.PIIRule) masker {
	t.Helper()
	m, err := newMasker(rules)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// 規約が無いときは、どの利用者にも当てはまる規則 (email、電話番号) だけを伏せる。
func TestMasker_Builtin(t *testing.T) {
	m := mustMasker(t, nil)
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"email", "連絡は foo.bar@example.com へ", "連絡は [EMAIL] へ"},
		{"phone jp", "電話は 090-1234-5678 まで", "電話は [PHONE] まで"},
		{"no match", "普通の文章です", "普通の文章です"},
		{"empty", "", ""},
		{"id-like text stays without policy", "会員 MEM-042 の件", "会員 MEM-042 の件"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := m.mask(tc.in); got != tc.want {
				t.Errorf("mask(%q) = %q; want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestMasker_PolicyRules(t *testing.T) {
	m := mustMasker(t, []policy.PIIRule{
		{Name: "member-id", Pattern: `\bMEM-\d{3,5}\b`, Replacement: "[MEMBER-ID]"},
		// 置き換えの文字列は literal として扱い、$1 のような展開をしない。
		{Name: "ticket", Pattern: `\bTKT-(\d+)\b`, Replacement: "[TICKET-$1]"},
	})
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"policy rule", "会員 MEM-042 の件", "会員 [MEMBER-ID] の件"},
		{"multiple", "MEM-001 と MEM-002", "[MEMBER-ID] と [MEMBER-ID]"},
		{"literal replacement", "TKT-77 を見る", "[TICKET-$1] を見る"},
		{"builtin still applies", "foo@example.com", "[EMAIL]"},
		{"lowercase not matched", "mem-042", "mem-042"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := m.mask(tc.in); got != tc.want {
				t.Errorf("mask(%q) = %q; want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestNewMasker_RejectsBadPattern(t *testing.T) {
	if _, err := newMasker([]policy.PIIRule{{Name: "x", Pattern: "(", Replacement: "[X]"}}); err == nil {
		t.Fatal("want error")
	}
}
