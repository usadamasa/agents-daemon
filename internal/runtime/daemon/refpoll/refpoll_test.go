package refpoll

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
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
	errs   map[string]error
	calls  []string
}

func (f *fakeRemote) fetch(_ context.Context, it ref.Item) (string, error) {
	key := it.Kind + ":" + it.Repo + "#" + strconv.Itoa(it.Number)
	f.calls = append(f.calls, key)
	if err := f.errs[key]; err != nil {
		return "", err
	}
	return f.states[key], nil
}

type fixture struct {
	p       *Poller
	refs    refstate.Store
	logPath string
	log     *applog.Logger
}

// newFixture は一時ディレクトリの辺と写しの置き場で Poller を作る。add で辺を書く。
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
	return fixture{p: New(edges, refs, remote.fetch, log), refs: refs, logPath: logPath, log: log}, add
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
