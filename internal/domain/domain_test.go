package domain

import "testing"

func TestClassify(t *testing.T) {
	work := New("/home/u", []string{"~/src/github.com/usadamasa", "/opt/private/"}, "")
	cases := []struct {
		name string
		c    Classifier
		cwd  string
		want Domain
	}{
		{"許可リストの下は私用", work, "/home/u/src/github.com/usadamasa/agents-daemon", Private},
		{"許可リストそのものも私用", work, "/home/u/src/github.com/usadamasa", Private},
		{"末尾の / を付けて書いた許可リストも効く", work, "/opt/private/notes", Private},
		{"名前の前方一致だけでは私用にしない", work, "/home/u/src/github.com/usadamasa-corp/app", Work},
		{"許可リストの外は業務", work, "/home/u/src/github.example.com/team/app", Work},
		{".. で許可リストの外へ出たら業務", work, "/home/u/src/github.com/usadamasa/../corp/app", Work},
		{"cwd が空なら業務", work, "", Work},
		{"相対パスの cwd は業務", work, "src/github.com/usadamasa/x", Work},
		{"私用の端末はどこでも私用", New("/home/u", nil, "private"), "/srv/anything", Private},
		{"私用の端末なら cwd が空でも私用", New("/home/u", nil, "private"), "", Private},
		{"ゼロ値はすべて業務", Classifier{}, "/home/u/src/github.com/usadamasa/x", Work},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.c.Classify(tc.cwd); got != tc.want {
				t.Errorf("Classify(%q) = %q, want %q", tc.cwd, got, tc.want)
			}
		})
	}
}

// 私用の範囲を意図せず広げる書き方 (相対パス、空) は捨てる。
func TestNew_dropsNonAbsolutePaths(t *testing.T) {
	c := New("/home/u", []string{"", "src/github.com/usadamasa", "~user/x", "~"}, "")
	got := c.PrivatePaths()
	if len(got) != 1 || got[0] != "/home/u" {
		t.Errorf("PrivatePaths() = %v, want [/home/u]", got)
	}
}

func TestParse(t *testing.T) {
	cases := map[string]Domain{"private": Private, "work": Work, "": Work, "Private": Work, "personal": Work}
	for in, want := range cases {
		if got := Parse(in); got != want {
			t.Errorf("Parse(%q) = %q, want %q", in, got, want)
		}
	}
}
