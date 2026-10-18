package harvest

import "testing"

func TestMaskPII(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"candidate id JP-TECH", "面接候補者 JP-TECH-042 の一次面接", "面接候補者 [CANDIDATE-ID] の一次面接"},
		{"candidate id US-DATA", "候補 US-DATA-1234", "候補 [CANDIDATE-ID]"},
		{"multiple ids", "JP-TECH-001 と JP-TECH-002", "[CANDIDATE-ID] と [CANDIDATE-ID]"},
		{"email", "連絡は foo.bar@example.com へ", "連絡は [EMAIL] へ"},
		{"phone jp", "電話は 090-1234-5678 まで", "電話は [PHONE] まで"},
		{"no match", "普通の文章です", "普通の文章です"},
		{"empty", "", ""},
		{"not candidate id (lowercase)", "jp-tech-042", "jp-tech-042"},
		{"not candidate id (too few digits)", "JP-TECH-1", "JP-TECH-1"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := MaskPII(tc.in)
			if got != tc.want {
				t.Errorf("MaskPII(%q) = %q; want %q", tc.in, got, tc.want)
			}
		})
	}
}
