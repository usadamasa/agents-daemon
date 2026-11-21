package refpoll

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/usadamasa/agents-daemon/internal/edge"
	"github.com/usadamasa/agents-daemon/internal/mention"
	"github.com/usadamasa/agents-daemon/internal/refstate"
	"github.com/usadamasa/agents-daemon/internal/tasklog/record"
	"github.com/usadamasa/agents-daemon/internal/worklog"
)

// fakeKinds は番号が issue か PR か。呼ばれた番号を remote.calls に「kind:owner/repo#N」で残す。
type fakeKinds struct {
	remote *fakeRemote
	kinds  map[string]string
	errs   map[string]error
}

func (k *fakeKinds) kind(_ context.Context, repo string, number int) (string, error) {
	key := repo + "#" + strconv.Itoa(number)
	k.remote.calls = append(k.remote.calls, "kind:"+key)
	if err := k.errs[key]; err != nil {
		return "", err
	}
	return k.kinds[key], nil
}

// noWorks は作業の記録が無いことを返す。
func noWorks() ([]worklog.Record, error) { return nil, nil }

// subjects は題ごとに 1 つのタスクの記録を返す関数。
func subjects(ss ...string) func() ([]record.Task, error) {
	return func() ([]record.Task, error) {
		tasks := make([]record.Task, 0, len(ss))
		for i, s := range ss {
			tasks = append(tasks, record.Task{ID: strconv.Itoa(i + 1), SessionID: "s1", Subject: s, Status: "pending", CWD: "/w"})
		}
		return tasks, nil
	}
}

// 引く言及は、タスクの記録と辺の記録から deps と同じ規則で拾う。cwd の owner/repo は記録の repo の鍵を先に使い、
// 同じ repo は 1 回だけ引く。鍵の無い記録は cwd を引く。
func TestListMentions(t *testing.T) {
	remote := &fakeRemote{}
	f, _ := newFixture(t, remote)
	if _, err := f.edges.Add(edge.Endpoint{Ref: "session:ad-1", CWD: "/w/ad"}, edge.Endpoint{Ref: "task:s1/1", CWD: "/w/ad"}, "#392 の PR の報告待ち", t0); err != nil {
		t.Fatal(err)
	}
	github := map[string]string{"/w/ws": "usadamasa/workspace", "/w/ad": "usadamasa/agents-daemon"}
	calls := map[string]int{}
	f.p.gitHub = func(path string) string { calls[path]++; return github[path] }
	f.sessions.places["/w/other"] = [2]string{"usadamasa/other", "main"}
	f.p.Mentions(func() ([]record.Task, error) {
		return []record.Task{
			{ID: "1", SessionID: "s1", Subject: "#469 の PR をマージ判断へ回す", CWD: "/w/ws-wt", Repo: "/w/ws"},
			{ID: "2", SessionID: "s1", Subject: "agents-daemon #366 を peer で処理", CWD: "/w/ws-wt", Repo: "/w/ws"},
			{ID: "3", SessionID: "s2", Subject: "#7 を見る", CWD: "/w/other"},
		}, nil
	}, noWorks, (&fakeKinds{remote: remote}).kind)
	edges, err := f.edges.Find("", "")
	if err != nil {
		t.Fatal(err)
	}
	want := []mention.Mention{
		{Repo: "usadamasa/agents-daemon", Number: 366}, {Repo: "usadamasa/agents-daemon", Number: 392},
		{Repo: "usadamasa/other", Number: 7}, {Repo: "usadamasa/workspace", Number: 469},
	}
	for range 2 {
		if got := f.p.listMentions(edges, map[string]refstate.Record{}, t0); !reflect.DeepEqual(got, want) {
			t.Errorf("mentions = %v, want %v", got, want)
		}
	}
	for path, n := range calls {
		if n != 1 {
			t.Errorf("%s の owner/repo を %d 回引いた (1 回のはず)", path, n)
		}
	}
}

// 題と note に書かれた番号 (言及) の題を引く (#474)。辺の端点を先に引き、言及は引く端点が無い tick に、種類を決めて
// から引く。言及だけの参照は写しがあれば引き直さない (open でも)。issue か PR かは引いた種類で写しに書く。
func TestRefresh_言及の題を引く(t *testing.T) {
	remote := &fakeRemote{
		states: map[string]string{"pr:o/r#1": "open", "pr:o/r#5": "open", "issue:o/r#6": "closed"},
		titles: map[string]string{"pr:o/r#5": "言及した PR", "issue:o/r#6": "言及した issue"},
	}
	kinds := &fakeKinds{remote: remote, kinds: map[string]string{"o/r#5": "pr", "o/r#6": "issue"}}
	f, add := newFixture(t, remote)
	add("pr:o/r#1", "sid:a")
	add("session:x", "sid:a")
	f.p.Mentions(subjects("o/r#5 と o/r#6 を見る"), noWorks, kinds.kind)

	for i := range 4 {
		f.p.Refresh(context.Background(), t0.Add(time.Duration(i)*time.Second))
	}
	want := []string{"pr:o/r#1", "kind:o/r#5", "pr:o/r#5", "kind:o/r#6", "issue:o/r#6"}
	if !reflect.DeepEqual(remote.calls, want) {
		t.Fatalf("calls = %v, want %v", remote.calls, want)
	}
	got, err := f.refs.List()
	if err != nil {
		t.Fatal(err)
	}
	if rec := got["pr:o/r#5"]; rec.Title != "言及した PR" || rec.State != refstate.StateOpen {
		t.Errorf("pr:o/r#5 の写し = %+v", rec)
	}
	if rec := got["issue:o/r#6"]; rec.Title != "言及した issue" {
		t.Errorf("issue:o/r#6 の写し = %+v", rec)
	}

	// 間隔が過ぎると open の端点は引き直すが、open の言及だけの参照は引き直さない。言及の写しは辺を変えない。
	remote.calls = nil
	for i := range 3 {
		f.p.Refresh(context.Background(), t0.Add(refstate.RefreshInterval+time.Duration(i)*time.Second))
	}
	if want := []string{"pr:o/r#1"}; !reflect.DeepEqual(remote.calls, want) {
		t.Errorf("間隔の後の calls = %v, want %v", remote.calls, want)
	}
	if got, want := edgeKeys(t, f.dir), []string{"pr:o/r#1>sid:a", "session:x>sid:a"}; !reflect.DeepEqual(got, want) {
		t.Errorf("辺 = %v, want %v", got, want)
	}
}

// 写しのある参照の番号は、種類と owner/repo の大文字小文字を問わず引かない。
func TestRefresh_写しのある言及は引かない(t *testing.T) {
	remote := &fakeRemote{states: map[string]string{"pr:O/R#7": "merged"}}
	kinds := &fakeKinds{remote: remote, kinds: map[string]string{}}
	f, add := newFixture(t, remote)
	add("pr:O/R#7", "sid:a")
	f.p.Mentions(subjects("o/r#7 を見る"), noWorks, kinds.kind)
	for i := range 3 {
		f.p.Refresh(context.Background(), t0.Add(time.Duration(i)*time.Second))
	}
	if want := []string{"pr:O/R#7"}; !reflect.DeepEqual(remote.calls, want) {
		t.Errorf("calls = %v, want %v", remote.calls, want)
	}
}

// 種類か状態を引けなかった言及は RefreshInterval のあいだ引かない (tick ごとに gh を呼ばない)。理由はログに 1 回。
func TestRefresh_言及の失敗(t *testing.T) {
	remote := &fakeRemote{errs: map[string]error{"issue:o/r#9": errors.New("rate limit")}}
	kinds := &fakeKinds{remote: remote, kinds: map[string]string{"o/r#9": "issue"}, errs: map[string]error{"o/r#8": errors.New("not found")}}
	f, _ := newFixture(t, remote)
	f.p.Mentions(subjects("o/r#8 と o/r#9 を見る"), noWorks, kinds.kind)
	for i := range 5 {
		f.p.Refresh(context.Background(), t0.Add(time.Duration(i)*time.Second))
	}
	if want := []string{"kind:o/r#8", "kind:o/r#9", "issue:o/r#9"}; !reflect.DeepEqual(remote.calls, want) {
		t.Fatalf("calls = %v, want %v", remote.calls, want)
	}
	if got := f.states(t); len(got) != 0 {
		t.Errorf("写し = %v (失敗した参照を書いてはいけない)", got)
	}
	f.p.Refresh(context.Background(), t0.Add(refstate.RefreshInterval+time.Second))
	if want := "kind:o/r#8"; remote.calls[len(remote.calls)-1] != want {
		t.Errorf("間隔の後の calls = %v", remote.calls)
	}
	if n := strings.Count(f.readLog(t), "not found"); n != 1 {
		t.Errorf("失敗のログが %d 行 (1 行のはず)", n)
	}
}

// gh の issue view の url から種類を決める。
func TestKindOfURL(t *testing.T) {
	for url, want := range map[string]string{
		"https://github.com/o/r/pull/664":  "pr",
		"https://github.com/o/r/issues/12": "issue",
	} {
		if got := kindOfURL(url); got != want {
			t.Errorf("kindOfURL(%q) = %q, want %q", url, got, want)
		}
	}
}
