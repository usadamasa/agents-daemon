package hookreport

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/usadamasa/agents-daemon/internal/herdrcli"
)

// fakeHerdr は HerdrClient の fake｡呼び出しを Calls に順に貯める｡
type fakeHerdr struct {
	Calls      []string
	pane       herdrcli.PaneView
	paneErr    error
	tab        herdrcli.TabView
	tabErr     error
	reportErr  error
	renameErr  error
	SocketPath string
	Tokens     map[string]string
	Seq        int64
	Source     string
}

func (f *fakeHerdr) PaneCurrent(_ context.Context, callerPaneID string) (herdrcli.PaneView, error) {
	f.Calls = append(f.Calls, "pane.current:"+callerPaneID)
	return f.pane, f.paneErr
}

func (f *fakeHerdr) ReportWorkspaceMetadata(_ context.Context, workspaceID, source string, seq int64, tokens map[string]string) error {
	f.Calls = append(f.Calls, "workspace.report_metadata:"+workspaceID)
	f.Source, f.Seq, f.Tokens = source, seq, tokens
	return f.reportErr
}

func (f *fakeHerdr) TabGet(_ context.Context, tabID string) (herdrcli.TabView, error) {
	f.Calls = append(f.Calls, "tab.get:"+tabID)
	return f.tab, f.tabErr
}

func (f *fakeHerdr) TabRename(_ context.Context, tabID, label string) error {
	f.Calls = append(f.Calls, "tab.rename:"+tabID+":"+label)
	return f.renameErr
}

func herdrEnv(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

var insideHerdr = map[string]string{
	"HERDR_ENV":         "1",
	"HERDR_SOCKET_PATH": "/tmp/h/herdr.sock",
	"HERDR_PANE_ID":     "w3X:p1",
}

// newTitleDeps は herdr の pane の中で動く既定の依存を作る｡branches は cwd → ブランチ名｡
func newTitleDeps(f *fakeHerdr, env map[string]string, branches map[string]string) TitleDeps {
	return TitleDeps{
		Getenv: herdrEnv(env),
		NewClient: func(socketPath string) HerdrClient {
			f.SocketPath = socketPath
			return f
		},
		Branch: func(_ context.Context, dir string) string { return branches[dir] },
		Now:    func() time.Time { return time.Unix(0, 1700000000000000000) },
	}
}

func TestTitle_OutsideHerdr_DoesNothing(t *testing.T) {
	cases := map[string]map[string]string{
		"環境変数なし":             {},
		"HERDR_ENV だけ":       {"HERDR_ENV": "1"},
		"socket が無い":         {"HERDR_ENV": "1", "HERDR_PANE_ID": "w3X:p1"},
		"pane が無い":           {"HERDR_ENV": "1", "HERDR_SOCKET_PATH": "/tmp/h/herdr.sock"},
		"HERDR_ENV が 1 ではない": {"HERDR_ENV": "0", "HERDR_SOCKET_PATH": "/tmp/h/herdr.sock", "HERDR_PANE_ID": "w3X:p1"},
	}
	for name, env := range cases {
		t.Run(name, func(t *testing.T) {
			f := &fakeHerdr{}
			reported, err := Title(context.Background(), newTitleDeps(f, env, nil))
			if err != nil || reported {
				t.Fatalf("Title() = (%v, %v), want (false, nil)", reported, err)
			}
			if len(f.Calls) != 0 {
				t.Errorf("herdr を呼んではいけない: %v", f.Calls)
			}
		})
	}
}

func TestTitle_InsideRexInsideHerdr_StillReports(t *testing.T) {
	env := map[string]string{"REX_SESSION": "s", "REX_BLOCK": "b"}
	for k, v := range insideHerdr {
		env[k] = v
	}
	f := &fakeHerdr{
		pane: herdrcli.PaneView{PaneID: "w3X:p1", TabID: "w3X:t1", WorkspaceID: "w3X", TerminalTitleStripped: "t", CWD: "/r"},
		tab:  herdrcli.TabView{TabID: "w3X:t1", Label: "1", Number: 1},
	}
	reported, err := Title(context.Background(), newTitleDeps(f, env, nil))
	if err != nil || !reported {
		t.Fatalf("Title() = (%v, %v), want (true, nil)", reported, err)
	}
}

func TestTitle_ReportsTitleAndBranchThenRenamesTab(t *testing.T) {
	f := &fakeHerdr{
		pane: herdrcli.PaneView{
			PaneID: "w3X:p1", TabID: "w3X:t1", WorkspaceID: "w3X",
			TerminalTitleStripped: "  fix   the\tbug ", CWD: "/repo", ForegroundCWD: "/repo/.claude/worktrees/wt",
		},
		tab: herdrcli.TabView{TabID: "w3X:t1", Label: "1", Number: 1},
	}
	branches := map[string]string{"/repo": "main", "/repo/.claude/worktrees/wt": "feat/x"}
	deps := newTitleDeps(f, insideHerdr, branches)

	reported, err := Title(context.Background(), deps)
	if err != nil || !reported {
		t.Fatalf("Title() = (%v, %v), want (true, nil)", reported, err)
	}
	if f.SocketPath != "/tmp/h/herdr.sock" {
		t.Errorf("socket path = %q, want HERDR_SOCKET_PATH", f.SocketPath)
	}
	wantCalls := []string{
		"pane.current:w3X:p1",
		"workspace.report_metadata:w3X",
		"tab.get:w3X:t1",
		"tab.rename:w3X:t1:fix the bug",
	}
	if strings.Join(f.Calls, "\n") != strings.Join(wantCalls, "\n") {
		t.Errorf("calls =\n%s\nwant\n%s", strings.Join(f.Calls, "\n"), strings.Join(wantCalls, "\n"))
	}
	if f.Source != TitleSource {
		t.Errorf("source = %q, want %q", f.Source, TitleSource)
	}
	if f.Seq != 1700000000000000000 {
		t.Errorf("seq = %d, want Now().UnixNano()", f.Seq)
	}
	// title は空白を 1 つに畳む (端末タイトルに改行やタブが混ざることがある)｡branch は foreground_cwd のもの｡
	if f.Tokens["claude"] != "fix the bug" || f.Tokens["branch"] != "feat/x" {
		t.Errorf("tokens = %v", f.Tokens)
	}
}

func TestTitle_BranchFallsBackToPaneCWD(t *testing.T) {
	// foreground_cwd が git の外 (worktree を消した直後など) なら pane を作った cwd のブランチを出す｡
	f := &fakeHerdr{
		pane: herdrcli.PaneView{PaneID: "w3X:p1", TabID: "w3X:t1", WorkspaceID: "w3X", TerminalTitleStripped: "t", CWD: "/repo", ForegroundCWD: "/elsewhere"},
		tab:  herdrcli.TabView{TabID: "w3X:t1", Label: "t", Number: 1},
	}
	if _, err := Title(context.Background(), newTitleDeps(f, insideHerdr, map[string]string{"/repo": "main"})); err != nil {
		t.Fatal(err)
	}
	if f.Tokens["branch"] != "main" {
		t.Errorf("tokens.branch = %q, want main", f.Tokens["branch"])
	}
}

func TestTitle_EmptyTitle_ClearsTokenAndRestoresTabNumber(t *testing.T) {
	f := &fakeHerdr{
		pane: herdrcli.PaneView{PaneID: "w3X:p1", TabID: "w3X:t2", WorkspaceID: "w3X", TerminalTitleStripped: "   ", CWD: "/none"},
		tab:  herdrcli.TabView{TabID: "w3X:t2", Label: "old title", Number: 2},
	}
	if _, err := Title(context.Background(), newTitleDeps(f, insideHerdr, nil)); err != nil {
		t.Fatal(err)
	}
	// 空の token は client が null で送って消す｡label は連番へ戻す (tab.rename に空を渡すと空の label になる)｡
	if v, ok := f.Tokens["claude"]; !ok || v != "" {
		t.Errorf("tokens.claude = %q (present=%v), want empty (clear)", v, ok)
	}
	if v, ok := f.Tokens["branch"]; !ok || v != "" {
		t.Errorf("tokens.branch = %q (present=%v), want empty (clear)", v, ok)
	}
	if last := f.Calls[len(f.Calls)-1]; last != "tab.rename:w3X:t2:2" {
		t.Errorf("last call = %q, want tab.rename to the tab number", last)
	}
}

func TestTitle_SkipsRenameWhenLabelAlreadyMatches(t *testing.T) {
	f := &fakeHerdr{
		pane: herdrcli.PaneView{PaneID: "w3X:p1", TabID: "w3X:t1", WorkspaceID: "w3X", TerminalTitleStripped: "same"},
		tab:  herdrcli.TabView{TabID: "w3X:t1", Label: "same", Number: 1},
	}
	if _, err := Title(context.Background(), newTitleDeps(f, insideHerdr, nil)); err != nil {
		t.Fatal(err)
	}
	for _, call := range f.Calls {
		if strings.HasPrefix(call, "tab.rename:") {
			t.Errorf("label が同じなら rename しない: %v", f.Calls)
		}
	}
}

func TestTitle_PaneWithoutTabOrWorkspace_DoesNothing(t *testing.T) {
	f := &fakeHerdr{pane: herdrcli.PaneView{PaneID: "w3X:p1", TerminalTitleStripped: "t"}}
	reported, err := Title(context.Background(), newTitleDeps(f, insideHerdr, nil))
	if err != nil || reported {
		t.Fatalf("Title() = (%v, %v), want (false, nil)", reported, err)
	}
	if len(f.Calls) != 1 {
		t.Errorf("pane.current の後に何も呼ばない: %v", f.Calls)
	}
}

func TestTitle_PaneCurrentFailure_IsReturned(t *testing.T) {
	boom := errors.New("pane not found")
	f := &fakeHerdr{paneErr: boom}
	_, err := Title(context.Background(), newTitleDeps(f, insideHerdr, nil))
	if !errors.Is(err, boom) {
		t.Fatalf("Title() error = %v, want %v", err, boom)
	}
}

func TestTitle_ReportFailure_StillAttemptsTab(t *testing.T) {
	// space 欄とタブ行は独立した面｡片方が失敗してももう片方は更新し､エラーは返す｡
	boom := errors.New("workspace gone")
	f := &fakeHerdr{
		pane:      herdrcli.PaneView{PaneID: "w3X:p1", TabID: "w3X:t1", WorkspaceID: "w3X", TerminalTitleStripped: "t"},
		tab:       herdrcli.TabView{TabID: "w3X:t1", Label: "1", Number: 1},
		reportErr: boom,
	}
	_, err := Title(context.Background(), newTitleDeps(f, insideHerdr, nil))
	if !errors.Is(err, boom) {
		t.Fatalf("Title() error = %v, want %v", err, boom)
	}
	if last := f.Calls[len(f.Calls)-1]; last != "tab.rename:w3X:t1:t" {
		t.Errorf("last call = %q, want tab.rename even after report failure", last)
	}
}

func TestGitBranch_ReturnsEmptyOutsideRepoOrForMissingDir(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if got := GitBranch(ctx, ""); got != "" {
		t.Errorf("GitBranch(\"\") = %q, want empty", got)
	}
	if got := GitBranch(ctx, t.TempDir()); got != "" {
		t.Errorf("GitBranch(empty dir) = %q, want empty", got)
	}
	if got := GitBranch(ctx, "/nonexistent/dir"); got != "" {
		t.Errorf("GitBranch(missing dir) = %q, want empty", got)
	}
}
