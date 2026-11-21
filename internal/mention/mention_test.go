package mention

import (
	"reflect"
	"testing"

	"github.com/usadamasa/agents-daemon/internal/edge"
	"github.com/usadamasa/agents-daemon/internal/tasklog/record"
	"github.com/usadamasa/agents-daemon/internal/worklog"
)

const (
	daemonRepo    = "usadamasa/agents-daemon"
	workspaceRepo = "usadamasa/workspace"
)

var known = []string{daemonRepo, workspaceRepo, "other/gh-manage"}

func TestText(t *testing.T) {
	for _, tc := range []struct {
		name     string
		text     string
		fallback string
		want     []Mention
	}{
		{"owner/repo#N はそのまま", "o/r#12 を見る", workspaceRepo, []Mention{{"o/r", 12}}},
		{"pr: / issue: の参照", "pr:o/r#3 と issue:o/r#4", workspaceRepo, []Mention{{"o/r", 3}, {"o/r", 4}}},
		{"素の #N は fallback で補う", "#469 の PR をマージ判断へ回す", workspaceRepo, []Mention{{workspaceRepo, 469}}},
		{"fallback が無ければ素の #N は拾わない", "#469 の PR", "", nil},
		{"repo#N は既知の repo の owner で補う", "agents-daemon#469 の PR", workspaceRepo, []Mention{{daemonRepo, 469}}},
		{"既知の repo でない語の直後の #N は素の #N", "dotfiles#7 を直す", workspaceRepo, []Mention{{workspaceRepo, 7}}},
		{"PR#N は素の #N", "PR#392 の報告待ち", workspaceRepo, []Mention{{workspaceRepo, 392}}},
		{"空白 1 つの repo #N は既知の repo なら repo#N", "agents-daemon #366 (根拠 1 セッション) を peer で処理", workspaceRepo, []Mention{{daemonRepo, 366}}},
		{"空白の前が既知の repo でなければ素の #N", "PR #392 の報告待ち", workspaceRepo, []Mention{{workspaceRepo, 392}}},
		{"空白 2 つは前置きにしない", "agents-daemon  #366", workspaceRepo, []Mention{{workspaceRepo, 366}}},
		{"既知の repo 名は大文字小文字を区別しない", "Agents-Daemon #5", workspaceRepo, []Mention{{daemonRepo, 5}}},
		{"素の #N は同じ文の番号の合う明示の参照の repo で補う", "o/r#5 と #5 と #6", workspaceRepo, []Mention{{"o/r", 5}, {workspaceRepo, 6}}},
		{"括弧や記号の後の #N", "PR A (#661+#662) → #663", daemonRepo, []Mention{{daemonRepo, 661}, {daemonRepo, 662}, {daemonRepo, 663}}},
		{"日本語の直後の #N", "題は#12で", daemonRepo, []Mention{{daemonRepo, 12}}},
		{"見出しの # と #0 と語の続く #N は拾わない", "# 題 #0 #12abc a#b", daemonRepo, nil},
		{"/ や & の直後の #N は拾わない", "#1/#2 &#3", daemonRepo, []Mention{{daemonRepo, 1}}},
		{"同じ言及は 1 つにする", "#7 と #7 と usadamasa/agents-daemon#7", daemonRepo, []Mention{{daemonRepo, 7}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := inText(tc.text, tc.fallback, known); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("inText(%q, %q) = %v, want %v", tc.text, tc.fallback, got, tc.want)
			}
		})
	}
}

func TestNote(t *testing.T) {
	for _, tc := range []struct {
		name string
		note string
		ends [2]end
		want []Mention
	}{
		{"番号の合う端点の参照の repo で補う", "#9 の PR",
			[2]end{{Ref: "pr:o/r#9", Repo: workspaceRepo}, {Ref: "sid:s1", Repo: workspaceRepo}}, []Mention{{"o/r", 9}}},
		{"両端が同じ番号で別 repo なら補わない", "#9",
			[2]end{{Ref: "pr:a/b#9"}, {Ref: "issue:c/d#9"}}, nil},
		{"合わなければ触れる repo が 1 つのときそれ", "#392 の PR の報告待ち",
			[2]end{{Ref: "session:ad-469", Repo: daemonRepo}, {Ref: "sid:s1", Repo: daemonRepo}}, []Mention{{daemonRepo, 392}}},
		{"触れる repo が 2 つなら補わない", "#392",
			[2]end{{Ref: "session:ad-469", Repo: daemonRepo}, {Ref: "sid:s1", Repo: workspaceRepo}}, nil},
		{"端点の repo が分からなければ補わない", "#392",
			[2]end{{Ref: "session:ad-469", Repo: daemonRepo}, {Ref: "sid:s1"}}, nil},
		{"repo#N は既知の repo で決まる", "agents-daemon#469 の PR",
			[2]end{{Ref: "session:ad-469", Repo: daemonRepo}, {Ref: "sid:s1", Repo: workspaceRepo}}, []Mention{{daemonRepo, 469}}},
		{"空白 1 つの repo #N も既知なら決まる", "agents-daemon #469 の PR",
			[2]end{{Ref: "session:ad-469", Repo: daemonRepo}, {Ref: "sid:s1", Repo: workspaceRepo}}, []Mention{{daemonRepo, 469}}},
		{"既知の repo でない語の直後の #N は素の #N", "dotfiles#7",
			[2]end{{Ref: "sid:s0", Repo: daemonRepo}, {Ref: "sid:s1", Repo: daemonRepo}}, []Mention{{daemonRepo, 7}}},
		{"既知の repo でない語の直後の #N は、触れる repo が 2 つなら補わない", "PR#7",
			[2]end{{Ref: "sid:s0", Repo: daemonRepo}, {Ref: "sid:s1", Repo: workspaceRepo}}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := inNote(tc.note, tc.ends, known); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("inNote(%q) = %v, want %v", tc.note, got, tc.want)
			}
		})
	}
}

// 作業の記録にだけ現れる repo も、repo 名で引ける (空白 1 つの repo #N の前置きになる)。
func TestCollect_作業の記録のrepo(t *testing.T) {
	github := map[string]string{"/w": workspaceRepo, "/g": "usadamasa/gh-manage"}
	got := Collect(Sources{
		Tasks:  []record.Task{{ID: "1", SessionID: "s1", Subject: "gh-manage #3 を見る", CWD: "/w"}},
		Works:  []worklog.Record{{SessionID: "s2", CWD: "/g-wt", Repo: "/g"}},
		GitHub: func(cwd, repo string) string { return github[repo] + github[cwd] },
	})
	if want := []Mention{{"usadamasa/gh-manage", 3}}; !reflect.DeepEqual(got.Tasks["task:s1/1"], want) {
		t.Errorf("Tasks = %v, want %v", got.Tasks, want)
	}
}

func TestMentionKey(t *testing.T) {
	if got := (Mention{"UsadaMasa/Agents-Daemon", 3}).Key(); got != "usadamasa/agents-daemon#3" {
		t.Errorf("Key() = %q", got)
	}
}

func TestCollect(t *testing.T) {
	github := map[string]string{"/w": workspaceRepo, "/d": daemonRepo}
	src := Sources{
		Tasks: []record.Task{
			{ID: "1", SessionID: "s1", Subject: "#469 の PR をマージ判断へ回す", CWD: "/w"},
			{ID: "2", SessionID: "s1", Subject: "gh-manage #3 を見る", CWD: "/w"},
			{ID: "3", SessionID: "s1", Subject: "番号の無い題", CWD: "/w"},
			{ID: "4", SessionID: "s2", Subject: "#5", CWD: "/gone"},
		},
		Edges: []edge.Edge{
			{ID: "e1", Before: edge.Endpoint{Ref: "session:ad-469", CWD: "/d"}, After: edge.Endpoint{Ref: "sid:s1", CWD: "/w"}, Note: "agents-daemon#469 の PR"},
			{ID: "e2", Before: edge.Endpoint{Ref: "pr:o/r#8", CWD: "/w"}, After: edge.Endpoint{Ref: "sid:s1", CWD: "/w"}, Note: "#8 のマージ"},
			{ID: "e3", Before: edge.Endpoint{Ref: "sid:s3", CWD: "/w"}, After: edge.Endpoint{Ref: "sid:s1", CWD: "/w"}},
		},
		Refs:   []string{"issue:other/gh-manage#1"},
		GitHub: func(cwd, _ string) string { return github[cwd] },
	}
	got := Collect(src)
	wantTasks := map[string][]Mention{
		"task:s1/1": {{workspaceRepo, 469}},
		"task:s1/2": {{"other/gh-manage", 3}},
	}
	if !reflect.DeepEqual(got.Tasks, wantTasks) {
		t.Errorf("Tasks = %v, want %v", got.Tasks, wantTasks)
	}
	wantEdges := map[string][]Mention{
		"e1": {{daemonRepo, 469}},
		"e2": {{"o/r", 8}},
	}
	if !reflect.DeepEqual(got.Edges, wantEdges) {
		t.Errorf("Edges = %v, want %v", got.Edges, wantEdges)
	}
	wantAll := []Mention{{"o/r", 8}, {"other/gh-manage", 3}, {daemonRepo, 469}, {workspaceRepo, 469}}
	if all := got.All(); !reflect.DeepEqual(all, wantAll) {
		t.Errorf("All() = %v, want %v", all, wantAll)
	}
}
