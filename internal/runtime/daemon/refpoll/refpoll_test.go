package refpoll

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/usadamasa/agents-daemon/internal/applog"
	"github.com/usadamasa/agents-daemon/internal/edge"
	"github.com/usadamasa/agents-daemon/internal/ref"
	"github.com/usadamasa/agents-daemon/internal/refstate"
)

var t0 = time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)

// fakeRemote は GitHub の側の状態。呼ばれた参照を順に残す。
type fakeRemote struct {
	states map[string]string
	titles map[string]string
	prs    map[string]refstate.PR
	heads  map[string]string
	errs   map[string]error
	calls  []string
}

func (f *fakeRemote) fetch(_ context.Context, it ref.Item) (Remote, error) {
	key := it.Kind + ":" + it.Repo + "#" + strconv.Itoa(it.Number)
	f.calls = append(f.calls, key)
	if err := f.errs[key]; err != nil {
		return Remote{}, err
	}
	return Remote{State: f.states[key], Title: f.titles[key], PR: f.prs[key], HeadBranch: f.heads[key]}, nil
}

// fakeSessions は生きているセッションと、その cwd の GitHub の repo と branch。locate の呼び出しを数える。
type fakeSessions struct {
	live    []Session
	places  map[string][2]string
	locates int
}

func (s *fakeSessions) list() []Session { return s.live }

func (s *fakeSessions) locate(cwd string) (string, string) {
	s.locates++
	p := s.places[cwd]
	return p[0], p[1]
}

type fixture struct {
	p        *Poller
	refs     refstate.Store
	edges    edge.Store
	dir      string
	logPath  string
	log      *applog.Logger
	sessions *fakeSessions
}

// newFixture は一時ディレクトリの辺と写しの置き場で Poller を作る。add で辺を書く。生きているセッションは
// f.sessions に置く (既定は無し)。
func newFixture(t *testing.T, remote *fakeRemote) (fixture, func(before, after string)) {
	t.Helper()
	dir := t.TempDir()
	logPath := filepath.Join(dir, "daemon.log")
	log := applog.NewLogger(logPath, time.Now)
	t.Cleanup(func() { _ = log.Close() })
	edges, refs := edge.New(dir), refstate.New(dir)
	add := func(before, after string) {
		t.Helper()
		if _, err := edges.Add(edge.Endpoint{Ref: before, CWD: "/w"}, edge.Endpoint{Ref: after, CWD: "/w"}, "", t0); err != nil {
			t.Fatal(err)
		}
	}
	sessions := &fakeSessions{places: map[string][2]string{}}
	p := New(edges, refs, remote.fetch, sessions.list, log)
	p.locate = sessions.locate
	return fixture{p: p, refs: refs, edges: edges, dir: dir, logPath: logPath, log: log, sessions: sessions}, add
}

func (f fixture) states(t *testing.T) map[string]string {
	t.Helper()
	got, err := f.refs.States()
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func (f fixture) readLog(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(f.logPath)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return string(data)
}

// 引き直しは 1 回に参照 1 つ。辺の端点の issue / PR (before と after の両方) だけを引き、まだ引いていないものから
// 順に引く。merged / closed は引き直さず、open は RefreshInterval ごとに引き直す (models/refstate の Due)。
func TestRefresh_間引きと引き直し(t *testing.T) {
	remote := &fakeRemote{states: map[string]string{"pr:o/r#1": "merged", "issue:o/r#2": "open"}}
	f, add := newFixture(t, remote)
	add("pr:o/r#1", "session:a")
	add("sid:b", "issue:o/r#2")
	add("session:a", "sid:b")

	f.p.Refresh(context.Background(), t0)
	f.p.Refresh(context.Background(), t0.Add(time.Second))
	f.p.Refresh(context.Background(), t0.Add(2*time.Second))
	if want := []string{"issue:o/r#2", "pr:o/r#1"}; !reflect.DeepEqual(remote.calls, want) {
		t.Fatalf("calls = %v, want %v", remote.calls, want)
	}
	if got := f.states(t); !reflect.DeepEqual(got, map[string]string{"pr:o/r#1": "merged", "issue:o/r#2": "open"}) {
		t.Errorf("写し = %v", got)
	}

	// open の参照は間隔が過ぎたら引き直し、そのときに閉じていれば写しを書き直す。merged は引かない。
	remote.states["issue:o/r#2"] = "closed"
	remote.calls = nil
	f.p.Refresh(context.Background(), t0.Add(refstate.RefreshInterval-time.Second))
	f.p.Refresh(context.Background(), t0.Add(refstate.RefreshInterval+time.Second))
	f.p.Refresh(context.Background(), t0.Add(100*refstate.RefreshInterval))
	if want := []string{"issue:o/r#2"}; !reflect.DeepEqual(remote.calls, want) {
		t.Errorf("calls = %v, want %v", remote.calls, want)
	}
	if got := f.states(t)["issue:o/r#2"]; got != "closed" {
		t.Errorf("issue:o/r#2 の写し = %q", got)
	}
}

// gh が失敗したら写しを書かない (models/refstate の NoFalseComplete)。同じ理由はログに 1 回だけ書く。失敗した参照は
// RefreshInterval のあいだ引かず、ほかの参照を先に引く (失敗し続ける参照が引き直しを塞がない)。
func TestRefresh_ghの失敗(t *testing.T) {
	fail := errors.New("gh が見つからない")
	remote := &fakeRemote{states: map[string]string{"pr:o/r#2": "merged"}, errs: map[string]error{"pr:o/r#1": fail}}
	f, add := newFixture(t, remote)
	add("pr:o/r#1", "sid:a")
	add("pr:o/r#2", "sid:a")

	for i := range 4 {
		f.p.Refresh(context.Background(), t0.Add(time.Duration(i)*time.Second))
	}
	if want := []string{"pr:o/r#1", "pr:o/r#2"}; !reflect.DeepEqual(remote.calls, want) {
		t.Errorf("calls = %v, want %v", remote.calls, want)
	}
	if got := f.states(t); !reflect.DeepEqual(got, map[string]string{"pr:o/r#2": "merged"}) {
		t.Errorf("写し = %v (失敗した参照を書いてはいけない)", got)
	}

	// 間隔が過ぎたら失敗した参照をまた引く。同じ理由ならログに書き直さない。
	f.p.Refresh(context.Background(), t0.Add(refstate.RefreshInterval+time.Second))
	if len(remote.calls) != 3 {
		t.Errorf("calls = %v", remote.calls)
	}
	if n := strings.Count(f.readLog(t), "gh が見つからない"); n != 1 {
		t.Errorf("失敗のログが %d 行 (1 行のはず)", n)
	}

	// 成功したら戻ったことをログに書き、写しを書く。
	delete(remote.errs, "pr:o/r#1")
	remote.states["pr:o/r#1"] = "open"
	f.p.Refresh(context.Background(), t0.Add(2*refstate.RefreshInterval+time.Second))
	if got := f.states(t)["pr:o/r#1"]; got != "open" {
		t.Errorf("pr:o/r#1 の写し = %q", got)
	}
	if !strings.Contains(f.readLog(t), "外部の参照の状態を引けるようになりました") {
		t.Errorf("戻ったことのログが無い: %q", f.readLog(t))
	}
}

// edgeKeys は残っている辺を「before>after」で返す (after は今の参照)。
func edgeKeys(t *testing.T, dir string) []string {
	t.Helper()
	edges, err := edge.New(dir).Find("", "")
	if err != nil {
		t.Fatal(err)
	}
	keys := []string{}
	for _, e := range edges {
		keys = append(keys, e.Before.Ref+">"+e.After.Ref)
	}
	sort.Strings(keys)
	return keys
}

// 写しが merged / closed の参照を before に持つ辺は、引き直しのたびに消す (models/refstate の EdgeRemoved)。
// 引いて済んだと分かった同じ引き直しで消し、済んだ後に書いた辺 (merged は引き直さない) も次の引き直しで消す。
// 結びつけた後の端点の辺も消える。消したことは daemon.log に 1 行書く。
func TestRefresh_済んだ参照の辺を消す(t *testing.T) {
	remote := &fakeRemote{states: map[string]string{"pr:o/r#1": "merged", "issue:o/r#2": "closed"}}
	f, add := newFixture(t, remote)
	add("pr:o/r#1", "sid:a")
	add("issue:o/r#2", "session:b")
	if _, err := f.edges.Bind(func(string, time.Time) (string, bool) { return "B", true }); err != nil {
		t.Fatal(err)
	}

	f.p.Refresh(context.Background(), t0)
	if got, want := edgeKeys(t, f.dir), []string{"pr:o/r#1>sid:a"}; !reflect.DeepEqual(got, want) {
		t.Errorf("1 回目の後の辺 = %v, want %v", got, want)
	}
	f.p.Refresh(context.Background(), t0.Add(time.Second))
	if got := edgeKeys(t, f.dir); len(got) != 0 {
		t.Errorf("2 回目の後の辺 = %v (済んだ参照の辺は消える)", got)
	}
	log := f.readLog(t)
	for _, want := range []string{"issue:o/r#2 → sid:B", "pr:o/r#1 → sid:a"} {
		if !strings.Contains(log, want) {
			t.Errorf("消したことのログに %q が無い: %q", want, log)
		}
	}

	// 済んだ後に書いた辺は、引き直さなくても次の引き直しで消える。
	add("pr:o/r#1", "sid:c")
	remote.calls = nil
	f.p.Refresh(context.Background(), t0.Add(2*time.Second))
	if got := edgeKeys(t, f.dir); len(got) != 0 {
		t.Errorf("済んだ後に書いた辺 = %v", got)
	}
	if len(remote.calls) != 0 {
		t.Errorf("merged の参照を引き直した: %v", remote.calls)
	}
}

// open の参照、まだ引いていない参照、gh で引けなかった参照を before に持つ辺は消さない
// (models/refstate の NoOpenRemoval)。済んだ参照が after にあるだけの辺も消さない (待っているのは before)。
func TestRefresh_済んでいない参照の辺は残す(t *testing.T) {
	remote := &fakeRemote{
		states: map[string]string{"pr:o/r#1": "open", "pr:o/r#3": "dropped", "pr:o/r#4": "merged"},
		errs:   map[string]error{"pr:o/r#2": errors.New("rate limit")},
	}
	f, add := newFixture(t, remote)
	add("pr:o/r#1", "sid:a")
	add("pr:o/r#2", "sid:a")
	add("pr:o/r#3", "sid:a")
	add("pr:o/r#9", "sid:a")
	add("sid:a", "pr:o/r#4")
	add("sid:a", "sid:b")

	// 参照の順に pr#1 (open)、pr#2 (失敗)、pr#3 (取りやめ)、pr#4 (merged) を引き、pr#9 はまだ引いていない。
	for i := range 4 {
		f.p.Refresh(context.Background(), t0.Add(time.Duration(i)*time.Second))
	}
	if want := []string{"pr:o/r#1", "pr:o/r#2", "pr:o/r#3", "pr:o/r#4"}; !reflect.DeepEqual(remote.calls, want) {
		t.Fatalf("calls = %v, want %v", remote.calls, want)
	}
	want := []string{"pr:o/r#1>sid:a", "pr:o/r#2>sid:a", "pr:o/r#3>sid:a", "pr:o/r#9>sid:a", "sid:a>pr:o/r#4", "sid:a>sid:b"}
	if got := edgeKeys(t, f.dir); !reflect.DeepEqual(got, want) {
		t.Errorf("辺 = %v, want %v", got, want)
	}
	// 取りやめは引き直さず、辺も残したまま。取りやめたことはログに 1 行書く。
	remote.calls = nil
	f.p.Refresh(context.Background(), t0.Add(100*refstate.RefreshInterval))
	for _, c := range remote.calls {
		if c == "pr:o/r#3" {
			t.Errorf("取りやめを引き直した: %v", remote.calls)
		}
	}
	if !strings.Contains(f.readLog(t), "pr:o/r#3 は取りやめ") {
		t.Errorf("取りやめのログが無い: %q", f.readLog(t))
	}
}

// gh の state (と issue の stateReason) を写しの状態にする。PR はマージしたかで、issue は閉じた理由で、済んだか
// 取りやめかを分ける (models/refstate の NoFalseComplete。CLOSED だけを見ると取りやめを完了と見せる)。
// 写しが open の参照を before に持つ辺は、Refresh のたびに mtime が進み、保持期間で消えない。引けていない参照の辺は進めない。
func TestRefresh_openの参照の辺の保持期間を数え直す(t *testing.T) {
	remote := &fakeRemote{
		states: map[string]string{"pr:o/r#1": "open"},
		errs:   map[string]error{"pr:o/r#2": errors.New("gh が失敗")},
	}
	f, add := newFixture(t, remote)
	add("pr:o/r#1", "task:a/1")
	add("pr:o/r#2", "task:a/2")
	edges, err := f.edges.Find("", "")
	if err != nil {
		t.Fatal(err)
	}
	stale := t0.Add(-edge.Retention - time.Hour)
	paths := map[string]string{}
	for _, e := range edges {
		paths[e.Before.Ref] = filepath.Join(f.edges.Dir, e.ID+".json")
		if err := os.Chtimes(paths[e.Before.Ref], stale, stale); err != nil {
			t.Fatal(err)
		}
	}

	// 1 回に 1 つ引くので、2 回で両方を引く。
	f.p.Refresh(context.Background(), t0)
	f.p.Refresh(context.Background(), t0)

	info, err := os.Stat(paths["pr:o/r#1"])
	if err != nil || !info.ModTime().Equal(t0) {
		t.Errorf("open の参照の辺の mtime = %v, %v, want %v", info.ModTime(), err, t0)
	}
	info, err = os.Stat(paths["pr:o/r#2"])
	if err != nil || !info.ModTime().Equal(stale) {
		t.Errorf("引けていない参照の辺の mtime = %v, %v, want %v", info.ModTime(), err, stale)
	}
}

func TestGHState(t *testing.T) {
	cases := []struct {
		kind, state, reason, want string
	}{
		{"pr", "OPEN", "", refstate.StateOpen},
		{"pr", "MERGED", "", refstate.StateMerged},
		{"pr", "CLOSED", "", refstate.StateDropped},
		{"issue", "OPEN", "REOPENED", refstate.StateOpen},
		{"issue", "CLOSED", "COMPLETED", refstate.StateClosed},
		{"issue", "CLOSED", "NOT_PLANNED", refstate.StateDropped},
		{"issue", "CLOSED", "DUPLICATE", refstate.StateDropped},
		// 理由の無い閉じた issue (理由ができる前に閉じたもの) は、GitHub の既定の理由 (completed) とみなす。
		{"issue", "CLOSED", "", refstate.StateClosed},
	}
	for _, c := range cases {
		got, err := ghState(c.kind, c.state, c.reason)
		if err != nil || got != c.want {
			t.Errorf("ghState(%q, %q, %q) = %q, %v, want %q", c.kind, c.state, c.reason, got, err, c.want)
		}
	}
	for _, c := range [][3]string{{"pr", "DRAFT", ""}, {"issue", "MERGED", ""}, {"pr", "", ""}} {
		if got, err := ghState(c[0], c[1], c[2]); err == nil {
			t.Errorf("ghState(%q, %q, %q) = %q, want error", c[0], c[1], c[2], got)
		}
	}
}

// 引いた題は状態と一緒に写しへ書く (#472)。引き直しで題が変わっていれば書き直す。
func TestRefresh_題を写しに書く(t *testing.T) {
	remote := &fakeRemote{states: map[string]string{"pr:o/r#1": "open"}, titles: map[string]string{"pr:o/r#1": "依存図に題を出す"}}
	f, add := newFixture(t, remote)
	add("pr:o/r#1", "sid:a")
	f.p.Refresh(context.Background(), t0)
	remote.titles["pr:o/r#1"] = "依存図に読める題を出す"
	f.p.Refresh(context.Background(), t0.Add(refstate.RefreshInterval))
	got, err := f.refs.List()
	if err != nil {
		t.Fatal(err)
	}
	if rec := got["pr:o/r#1"]; rec.Title != "依存図に読める題を出す" || rec.State != refstate.StateOpen {
		t.Errorf("写し = %+v", rec)
	}
}

// gh の view の JSON から状態と題を読む。
func TestDecodeView(t *testing.T) {
	got, err := decodeView("pr", []byte(`{"state":"MERGED","title":"題を引く"}`))
	if err != nil || got != (Remote{State: refstate.StateMerged, Title: "題を引く"}) {
		t.Errorf("decodeView(pr) = %+v, %v", got, err)
	}
	got, err = decodeView("issue", []byte(`{"state":"CLOSED","stateReason":"NOT_PLANNED","title":"やめた"}`))
	if err != nil || got != (Remote{State: refstate.StateDropped, Title: "やめた"}) {
		t.Errorf("decodeView(issue) = %+v, %v", got, err)
	}
	if got, err := decodeView("pr", []byte(`{`)); err == nil {
		t.Errorf("壊れた JSON = %+v, want error", got)
	}
}

// PR の head branch を読む。マージ後も読む (初めて引いた時点でマージ済みの PR にも導く)。
func TestDecodeView_PRのheadBranch(t *testing.T) {
	for _, state := range []string{"OPEN", "MERGED", "CLOSED"} {
		got, err := decodeView("pr", []byte(`{"state":"`+state+`","title":"t","headRefName":"worktree-x"}`))
		if err != nil || got.HeadBranch != "worktree-x" {
			t.Errorf("decodeView(pr %s) = %+v, %v", state, got, err)
		}
	}
}

// 写しに head branch を書く。
func TestRefresh_headBranchを写しに書く(t *testing.T) {
	remote := &fakeRemote{states: map[string]string{"pr:o/r#1": "open"}, heads: map[string]string{"pr:o/r#1": "feat"}}
	f, add := newFixture(t, remote)
	add("pr:o/r#1", "sid:h")

	f.p.Refresh(context.Background(), t0)
	got, err := f.refs.List()
	if err != nil {
		t.Fatal(err)
	}
	if got["pr:o/r#1"].HeadBranch != "feat" {
		t.Errorf("写し = %+v", got["pr:o/r#1"])
	}
}

// 渡す側が pr → task だけを書いた PR に、PR の repo と head branch で動いている生きているセッションから PR への辺を
// 導く (#487、models/prlink の EventuallyAnchored)。repo が違うセッション、合うセッションが 2 つ以上の PR には導かない。
func TestRefresh_PRのbranchのセッションからPRへ辺を導く(t *testing.T) {
	remote := &fakeRemote{
		states: map[string]string{"pr:o/r#1": "open", "pr:o/r#2": "open", "pr:o/r#3": "open"},
		heads:  map[string]string{"pr:o/r#1": "feat", "pr:o/r#2": "dup", "pr:o/r#3": "other"},
	}
	f, add := newFixture(t, remote)
	add("pr:o/r#1", "task:H/1")
	add("pr:o/r#2", "task:H/1")
	add("pr:o/r#3", "task:H/1")
	f.sessions.live = []Session{{ID: "A", CWD: "/a"}, {ID: "B", CWD: "/b"}, {ID: "C", CWD: "/c"}, {ID: "D", CWD: "/d"}, {ID: "E", CWD: "/e"}}
	f.sessions.places = map[string][2]string{
		"/a": {"O/R", "feat"},  // repo の大文字小文字は区別しない
		"/b": {"o/r", "main"},  // branch が違う
		"/c": {"x/y", "other"}, // repo が違う
		"/d": {"o/r", "dup"},
		"/e": {"o/r", "dup"},
	}

	for i := range 3 {
		f.p.Refresh(context.Background(), t0.Add(time.Duration(i)*time.Second))
	}
	got := edgeKeys(t, f.dir)
	want := []string{"pr:o/r#1>task:H/1", "pr:o/r#2>task:H/1", "pr:o/r#3>task:H/1", "sid:A>pr:o/r#1"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("辺 = %v, want %v", got, want)
	}
	if log := f.readLog(t); !strings.Contains(log, "sid:A → pr:o/r#1") {
		t.Errorf("導いたことのログが無い: %q", log)
	}
}

// 初めて引いた時点でマージ済みの PR も、同じ引き直しで pr → task を消す前に導く。導いた辺は PR が済んでも残り、
// PR のノードは図に残る (models/prlink の PRKept)。
func TestRefresh_マージ済みのPRも掃除の前に導く(t *testing.T) {
	remote := &fakeRemote{states: map[string]string{"pr:o/r#1": "merged"}, heads: map[string]string{"pr:o/r#1": "feat"}}
	f, add := newFixture(t, remote)
	add("pr:o/r#1", "task:H/1")
	f.sessions.live = []Session{{ID: "A", CWD: "/a"}}
	f.sessions.places["/a"] = [2]string{"o/r", "feat"}

	f.p.Refresh(context.Background(), t0)
	if got, want := edgeKeys(t, f.dir), []string{"sid:A>pr:o/r#1"}; !reflect.DeepEqual(got, want) {
		t.Errorf("辺 = %v, want %v", got, want)
	}
}

// 別の PR の照合で branch を引いた直後に PR の branch へ切り替え、locateTTL の内に初めて引いた PR が既にマージ済みでも、
// 済んだ候補は使い回さずに branch を引き直して掃除の前に導く (#577、models/prlink の PRKept)。
func TestRefresh_branchを切り替えた直後に初めて引いたマージ済みのPRも導く(t *testing.T) {
	remote := &fakeRemote{
		states: map[string]string{"pr:o/r#1": "open", "pr:o/r#2": "merged"},
		heads:  map[string]string{"pr:o/r#1": "other", "pr:o/r#2": "feat"},
	}
	f, add := newFixture(t, remote)
	add("pr:o/r#1", "task:H/1")
	f.sessions.live = []Session{{ID: "A", CWD: "/a"}}
	f.sessions.places["/a"] = [2]string{"o/r", "main"}

	f.p.Refresh(context.Background(), t0)
	if f.sessions.locates != 1 {
		t.Fatalf("前提: 別の PR の照合で branch を引く (%d 回)", f.sessions.locates)
	}

	f.sessions.places["/a"] = [2]string{"o/r", "feat"}
	add("pr:o/r#2", "task:H/2")
	f.p.Refresh(context.Background(), t0.Add(5*time.Second))
	got := edgeKeys(t, f.dir)
	want := []string{"pr:o/r#1>task:H/1", "sid:A>pr:o/r#2"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("辺 = %v, want %v", got, want)
	}
}

// 取りやめた PR の辺は掃除しないので、照合は locateTTL のあいだ branch を使い回す (tick ごとに git を起こさない)。
func TestRefresh_取りやめたPRの照合ではbranchを使い回す(t *testing.T) {
	remote := &fakeRemote{states: map[string]string{"pr:o/r#1": "dropped"}, heads: map[string]string{"pr:o/r#1": "feat"}}
	f, add := newFixture(t, remote)
	add("pr:o/r#1", "task:H/1")
	f.sessions.live = []Session{{ID: "A", CWD: "/a"}}
	f.sessions.places["/a"] = [2]string{"o/r", "main"}

	for i := range 3 {
		f.p.Refresh(context.Background(), t0.Add(time.Duration(i)*5*time.Second))
	}
	if f.sessions.locates != 1 {
		t.Errorf("locateTTL の内で branch を %d 回引いた, want 1", f.sessions.locates)
	}
}

// 自分の PR のマージを待つセッションには導かず、導いた後に待つ辺を書いたら導いた辺を消す (models/prlink の NoDaemonLoop)。
func TestRefresh_自分のPRを待つ形になったら導いた辺を消す(t *testing.T) {
	remote := &fakeRemote{states: map[string]string{"pr:o/r#1": "open"}, heads: map[string]string{"pr:o/r#1": "feat"}}
	f, add := newFixture(t, remote)
	add("pr:o/r#1", "task:H/1")
	f.sessions.live = []Session{{ID: "A", CWD: "/a"}}
	f.sessions.places["/a"] = [2]string{"o/r", "feat"}

	f.p.Refresh(context.Background(), t0)
	add("pr:o/r#1", "task:A/2")
	f.p.Refresh(context.Background(), t0.Add(time.Second))
	if got, want := edgeKeys(t, f.dir), []string{"pr:o/r#1>task:A/2", "pr:o/r#1>task:H/1"}; !reflect.DeepEqual(got, want) {
		t.Errorf("辺 = %v, want %v", got, want)
	}
}

// 導く候補 (head branch が分かっていて上流の無い PR) が無ければ、セッションの branch を git で引かない。
// 引いた branch は locateTTL のあいだ使い回す。
func TestRefresh_branchを引くのは候補があるときだけ(t *testing.T) {
	remote := &fakeRemote{states: map[string]string{"pr:o/r#1": "open"}, heads: map[string]string{"pr:o/r#1": "feat"}}
	f, add := newFixture(t, remote)
	add("sid:x", "pr:o/r#1")
	add("pr:o/r#1", "task:H/1")
	f.sessions.live = []Session{{ID: "A", CWD: "/a"}}

	f.p.Refresh(context.Background(), t0)
	if f.sessions.locates != 0 {
		t.Errorf("上流のある PR だけなのに branch を %d 回引いた", f.sessions.locates)
	}

	if _, err := f.edges.Remove(edge.Endpoint{Ref: "sid:x", CWD: "/w"}, edge.Endpoint{Ref: "pr:o/r#1", CWD: "/w"}); err != nil {
		t.Fatal(err)
	}
	f.p.Refresh(context.Background(), t0.Add(time.Second))
	f.p.Refresh(context.Background(), t0.Add(2*time.Second))
	if f.sessions.locates != 1 {
		t.Errorf("locateTTL の内で branch を %d 回引いた, want 1", f.sessions.locates)
	}
	f.p.Refresh(context.Background(), t0.Add(locateTTL+3*time.Second))
	if f.sessions.locates != 2 {
		t.Errorf("locateTTL の後に引き直していない (%d 回)", f.sessions.locates)
	}
}

// usadamasa/agents-daemon#540 の gh pr view --json の実物。pr-labels の sync は同じ commit で 2 回走り、先の run が
// concurrency で CANCELLED になっている。最新の run だけを見るので CI 失敗にしない。
const pr540 = `{"state":"OPEN","title":"Release for 2026.1109.3","isDraft":false,"mergeStateStatus":"CLEAN","mergeable":"MERGEABLE","reviewDecision":"",
"statusCheckRollup":[
{"__typename":"CheckRun","completedAt":"2026-11-09T08:15:10Z","conclusion":"CANCELLED","detailsUrl":"https://github.com/usadamasa/agents-daemon/actions/runs/42584585698/job/129051231200","name":"sync","startedAt":"2026-11-09T08:15:10Z","status":"COMPLETED","workflowName":"pr-labels"},
{"__typename":"CheckRun","completedAt":"2026-11-09T08:16:10Z","conclusion":"SUCCESS","name":"go test","startedAt":"2026-11-09T08:15:25Z","status":"COMPLETED","workflowName":"ci"},
{"__typename":"CheckRun","completedAt":"2026-11-09T08:15:20Z","conclusion":"SUCCESS","name":"sync","startedAt":"2026-11-09T08:15:14Z","status":"COMPLETED","workflowName":"pr-labels"},
{"__typename":"CheckRun","completedAt":"2026-11-09T08:15:43Z","conclusion":"SKIPPED","name":"gui test","startedAt":"2026-11-09T08:15:43Z","status":"COMPLETED","workflowName":"ci"}
]}`

func TestDecodeView_実物のrollupの重複は最新だけを見る(t *testing.T) {
	got, err := decodeView("pr", []byte(pr540))
	if err != nil {
		t.Fatal(err)
	}
	want := Remote{State: refstate.StateOpen, Title: "Release for 2026.1109.3", PR: refstate.PR{CI: refstate.CIPassing, Merge: refstate.MergeClean}}
	if got != want {
		t.Errorf("decodeView = %+v, want %+v", got, want)
	}
}

// statusCheckRollup の 1 行。CheckRun は status と conclusion、StatusContext は state と context を持つ (gh の実物の形)。
func checkRun(workflow, name, status, conclusion, started string) string {
	return `{"__typename":"CheckRun","workflowName":"` + workflow + `","name":"` + name + `","status":"` + status + `","conclusion":"` + conclusion + `","startedAt":"` + started + `"}`
}

func statusContext(context, state string) string {
	return `{"__typename":"StatusContext","context":"` + context + `","state":"` + state + `","startedAt":"2026-10-22T08:12:59Z","targetUrl":""}`
}

// notStarted は gh が開始前の check に書く startedAt。
const notStarted = "0001-01-01T00:00:00Z"

func TestDecodeView_CIを1語に畳む(t *testing.T) {
	const at1, at2 = "2026-11-09T08:00:00Z", "2026-11-09T08:05:00Z"
	done := func(name, conclusion string) string { return checkRun("ci", name, "COMPLETED", conclusion, at1) }
	cases := []struct {
		name   string
		checks []string
		want   string
	}{
		{"check が無ければ通過と言わない", nil, ""},
		{"成功", []string{done("a", "SUCCESS")}, refstate.CIPassing},
		{"NEUTRAL と SKIPPED は通過", []string{done("a", "NEUTRAL"), done("b", "SKIPPED")}, refstate.CIPassing},
		{"FAILURE", []string{done("a", "SUCCESS"), done("b", "FAILURE")}, refstate.CIFailing},
		{"TIMED_OUT", []string{done("a", "TIMED_OUT")}, refstate.CIFailing},
		{"ACTION_REQUIRED", []string{done("a", "ACTION_REQUIRED")}, refstate.CIFailing},
		{"STARTUP_FAILURE", []string{done("a", "STARTUP_FAILURE")}, refstate.CIFailing},
		{"後の run が無い CANCELLED は失敗", []string{done("a", "CANCELLED")}, refstate.CIFailing},
		{"実行中", []string{done("a", "SUCCESS"), checkRun("ci", "b", "IN_PROGRESS", "", at1)}, refstate.CIRunning},
		{"順番待ち", []string{checkRun("ci", "b", "QUEUED", "", notStarted)}, refstate.CIRunning},
		{"失敗は実行中より強い", []string{done("a", "FAILURE"), checkRun("ci", "b", "IN_PROGRESS", "", at1)}, refstate.CIFailing},
		{"失敗の後の成功は通過", []string{checkRun("ci", "a", "COMPLETED", "FAILURE", at1), checkRun("ci", "a", "COMPLETED", "SUCCESS", at2)}, refstate.CIPassing},
		{"成功の後の失敗は失敗 (並びによらない)", []string{checkRun("ci", "a", "COMPLETED", "FAILURE", at2), checkRun("ci", "a", "COMPLETED", "SUCCESS", at1)}, refstate.CIFailing},
		{"失敗の後の開始前の再実行は実行中", []string{checkRun("ci", "a", "COMPLETED", "FAILURE", at1), checkRun("ci", "a", "QUEUED", "", notStarted)}, refstate.CIRunning},
		{"workflow が違えば同じ名前でも別の check", []string{checkRun("ci", "a", "COMPLETED", "FAILURE", at1), checkRun("other", "a", "COMPLETED", "SUCCESS", at2)}, refstate.CIFailing},
		{"StatusContext の成功", []string{statusContext("license/cla", "SUCCESS")}, refstate.CIPassing},
		{"StatusContext の失敗", []string{statusContext("a", "FAILURE")}, refstate.CIFailing},
		{"StatusContext の ERROR", []string{statusContext("a", "ERROR")}, refstate.CIFailing},
		{"StatusContext の待ち", []string{statusContext("a", "PENDING")}, refstate.CIRunning},
		{"StatusContext の EXPECTED", []string{statusContext("a", "EXPECTED")}, refstate.CIRunning},
		{"知らない語を通過にしない", []string{done("a", "SOMETHING_NEW")}, refstate.CIRunning},
	}
	for _, c := range cases {
		out := `{"state":"OPEN","title":"t","statusCheckRollup":[` + strings.Join(c.checks, ",") + `]}`
		got, err := decodeView("pr", []byte(out))
		if err != nil || got.PR.CI != c.want {
			t.Errorf("%s: CI = %q, %v, want %q", c.name, got.PR.CI, err, c.want)
		}
	}
}

func TestDecodeView_Draftとレビューとマージ(t *testing.T) {
	cases := []struct {
		name string
		json string
		want refstate.PR
	}{
		{"Draft", `"isDraft":true,"mergeStateStatus":"DRAFT","mergeable":"MERGEABLE"`, refstate.PR{Draft: true}},
		{"approve 待ち", `"reviewDecision":"REVIEW_REQUIRED","mergeStateStatus":"BLOCKED","mergeable":"MERGEABLE"`, refstate.PR{Review: refstate.ReviewRequired}},
		{"変更要求", `"reviewDecision":"CHANGES_REQUESTED"`, refstate.PR{Review: refstate.ReviewChangesRequested}},
		{"approved", `"reviewDecision":"APPROVED"`, refstate.PR{Review: refstate.ReviewApproved}},
		{"レビューの要らない repo は語なし", `"reviewDecision":""`, refstate.PR{}},
		{"衝突", `"mergeStateStatus":"DIRTY","mergeable":"CONFLICTING"`, refstate.PR{Merge: refstate.MergeConflicting}},
		{"Draft の衝突は mergeStateStatus によらない", `"isDraft":true,"mergeStateStatus":"DRAFT","mergeable":"CONFLICTING"`, refstate.PR{Draft: true, Merge: refstate.MergeConflicting}},
		{"base より古い", `"mergeStateStatus":"BEHIND","mergeable":"MERGEABLE"`, refstate.PR{Merge: refstate.MergeBehind}},
		{"マージ可", `"mergeStateStatus":"CLEAN","mergeable":"MERGEABLE"`, refstate.PR{Merge: refstate.MergeClean}},
		{"計算中はマージ可と言わない", `"mergeStateStatus":"UNKNOWN","mergeable":"UNKNOWN"`, refstate.PR{}},
		{"BLOCKED はマージ可と言わない", `"mergeStateStatus":"BLOCKED","mergeable":"MERGEABLE"`, refstate.PR{}},
		{"UNSTABLE はマージ可と言わない", `"mergeStateStatus":"UNSTABLE","mergeable":"MERGEABLE"`, refstate.PR{}},
	}
	for _, c := range cases {
		got, err := decodeView("pr", []byte(`{"state":"OPEN","title":"t",`+c.json+`}`))
		if err != nil || got.PR != c.want {
			t.Errorf("%s: PR = %+v, %v, want %+v", c.name, got.PR, err, c.want)
		}
	}
}

// open でない PR と issue には段を持たせない。
func TestDecodeView_openのPRだけ段を持つ(t *testing.T) {
	const fields = `"title":"t","isDraft":true,"reviewDecision":"APPROVED","mergeStateStatus":"CLEAN","statusCheckRollup":[` + `{"__typename":"StatusContext","context":"a","state":"SUCCESS","startedAt":"2026-10-22T08:12:59Z"}` + `]`
	for _, c := range [][2]string{{"pr", "MERGED"}, {"pr", "CLOSED"}, {"issue", "OPEN"}} {
		got, err := decodeView(c[0], []byte(`{"state":"`+c[1]+`",`+fields+`}`))
		if err != nil || got.PR != (refstate.PR{}) {
			t.Errorf("%s %s: PR = %+v, %v", c[0], c[1], got.PR, err)
		}
	}
}

// 引いた段は状態と一緒に写しへ書く。引き直しで変わっていれば書き直す。
func TestRefresh_段を写しに書く(t *testing.T) {
	remote := &fakeRemote{states: map[string]string{"pr:o/r#1": "open"}, prs: map[string]refstate.PR{"pr:o/r#1": {CI: refstate.CIRunning}}}
	f, add := newFixture(t, remote)
	add("pr:o/r#1", "sid:a")
	f.p.Refresh(context.Background(), t0)
	remote.prs["pr:o/r#1"] = refstate.PR{CI: refstate.CIFailing}
	f.p.Refresh(context.Background(), t0.Add(refstate.RefreshInterval))
	got, err := f.refs.List()
	if err != nil {
		t.Fatal(err)
	}
	if rec := got["pr:o/r#1"]; rec.PR != (refstate.PR{CI: refstate.CIFailing}) {
		t.Errorf("写し = %+v", rec)
	}
}

// nil の Poller (daemon の runtime のゼロ値) は何もしない。
func TestRefresh_nil(t *testing.T) {
	var p *Poller
	p.Refresh(context.Background(), t0)
}

// 済んだ状態を写しに書いたときはログに 1 行残す (依存図の参照が完了に変わった理由を追えるようにする)。
func TestRefresh_済んだことをログに書く(t *testing.T) {
	remote := &fakeRemote{states: map[string]string{"pr:o/r#1": "merged"}}
	f, add := newFixture(t, remote)
	add("pr:o/r#1", "sid:a")
	f.p.Refresh(context.Background(), t0)
	if !strings.Contains(f.readLog(t), "pr:o/r#1 は merged") {
		t.Errorf("ログ = %q", f.readLog(t))
	}
}
