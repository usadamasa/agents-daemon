package dreamstate

import "testing"

func TestCanonicalizeProjectPath(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"/Users/masaru_uchida/workspace", "-Users-masaru-uchida-workspace"},
		{"/home/x/repo", "-home-x-repo"},
		{"/Users/x/src/github.com/y/z", "-Users-x-src-github-com-y-z"},
		{"", ""},
	}
	for _, tc := range tests {
		got := CanonicalizeProjectPath(tc.in)
		if got != tc.want {
			t.Errorf("CanonicalizeProjectPath(%q) = %q; want %q", tc.in, got, tc.want)
		}
	}
}

func TestResolveProject(t *testing.T) {
	tests := []struct {
		name    string
		flag    string
		cwd     string
		want    string
		wantErr bool
	}{
		{
			name: "flag with canonical name passes through",
			flag: "-Users-masaru-uchida-workspace",
			cwd:  "/tmp/somewhere",
			want: "-Users-masaru-uchida-workspace",
		},
		{
			name: "flag with absolute path is canonicalized",
			flag: "/Users/masaru_uchida/workspace",
			cwd:  "/tmp/somewhere",
			want: "-Users-masaru-uchida-workspace",
		},
		{
			name: "no flag falls back to cwd canonical",
			flag: "",
			cwd:  "/Users/masaru_uchida/workspace",
			want: "-Users-masaru-uchida-workspace",
		},
		{
			name: "dots in path are canonicalized to hyphens",
			flag: "/Users/masaru_uchida/src/github.com/usadamasa/claude-config",
			cwd:  "",
			want: "-Users-masaru-uchida-src-github-com-usadamasa-claude-config",
		},
		{
			name: "cwd fallback also canonicalizes dots",
			flag: "",
			cwd:  "/Users/x/src/github.com/y/z",
			want: "-Users-x-src-github-com-y-z",
		},
		{
			name:    "no flag and no cwd returns error",
			flag:    "",
			cwd:     "",
			wantErr: true,
		},
		{
			name:    "relative flag is rejected",
			flag:    "some/rel/path",
			cwd:     "/tmp",
			wantErr: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ResolveProject(tc.flag, tc.cwd)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ResolveProject(%q, %q) expected error, got %q", tc.flag, tc.cwd, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolveProject(%q, %q) unexpected error: %v", tc.flag, tc.cwd, err)
			}
			if got != tc.want {
				t.Errorf("ResolveProject(%q, %q) = %q; want %q", tc.flag, tc.cwd, got, tc.want)
			}
		})
	}
}
