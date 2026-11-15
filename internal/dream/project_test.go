package dream

import "testing"

func TestCanonicalizeProjectPath(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"/Users/jane_doe/workspace", "-Users-jane-doe-workspace"},
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
			flag: "-Users-jane-doe-workspace",
			cwd:  "/tmp/somewhere",
			want: "-Users-jane-doe-workspace",
		},
		{
			name: "flag with absolute path is canonicalized",
			flag: "/Users/jane_doe/workspace",
			cwd:  "/tmp/somewhere",
			want: "-Users-jane-doe-workspace",
		},
		{
			name: "no flag falls back to cwd canonical",
			flag: "",
			cwd:  "/Users/jane_doe/workspace",
			want: "-Users-jane-doe-workspace",
		},
		{
			name: "dots in path are canonicalized to hyphens",
			flag: "/Users/jane_doe/src/github.com/example/my.repo",
			cwd:  "",
			want: "-Users-jane-doe-src-github-com-example-my-repo",
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
