package daemon

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/usadamasa/agents-daemon/internal/edge"
	"github.com/usadamasa/agents-daemon/internal/multiplexer"
	"github.com/usadamasa/agents-daemon/internal/multiplexer/multiplexerfake"
	"github.com/usadamasa/agents-daemon/internal/ref"
	"github.com/usadamasa/agents-daemon/internal/refstate"
)

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

// newRefRuntime は辺の置き場と写しの置き場を持つ daemonRuntime と、辺を書く関数を返す。
func newRefRuntime(t *testing.T, remote *fakeRemote, now time.Time) (*daemonRuntime, string, func(before, after string)) {
	t.Helper()
	r, statusPath := newTestRuntime(t, &multiplexerfake.Client{}, now)
	dir := filepath.Dir(statusPath)
	r.attachStateDir(dir)
	r.fetchRef = remote.fetch
	add := func(before, after string) {
		t.Helper()
		if _, err := edge.New(dir).Add(edge.Endpoint{Ref: before, CWD: "/w"}, edge.Endpoint{Ref: after, CWD: "/w"}, "", now); err != nil {
			t.Fatal(err)
		}
	}
	return r, statusPath, add
}

func states(t *testing.T, r *daemonRuntime) map[string]string {
	t.Helper()
	got, err := r.refs.States()
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// 引き直しは 1 回に参照 1 つ。辺の端点の issue / PR (before と after の両方) だけを引き、まだ引いていないものから
// 順に引く。merged / closed は引き直さず、open は RefreshInterval ごとに引き直す (models/refstate の Due)。
func TestRefreshRefs_間引きと引き直し(t *testing.T) {
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	remote := &fakeRemote{states: map[string]string{"pr:o/r#1": "merged", "issue:o/r#2": "open"}}
	r, _, add := newRefRuntime(t, remote, now)
	add("pr:o/r#1", "session:a")
	add("sid:b", "issue:o/r#2")
	add("session:a", "sid:b")

	r.refreshRefs(context.Background(), now)
	r.refreshRefs(context.Background(), now.Add(time.Second))
	r.refreshRefs(context.Background(), now.Add(2*time.Second))
	if want := []string{"issue:o/r#2", "pr:o/r#1"}; !reflect.DeepEqual(remote.calls, want) {
		t.Fatalf("calls = %v, want %v", remote.calls, want)
	}
	if got := states(t, r); !reflect.DeepEqual(got, map[string]string{"pr:o/r#1": "merged", "issue:o/r#2": "open"}) {
		t.Errorf("写し = %v", got)
	}

	// open の参照は間隔が過ぎたら引き直し、そのときに閉じていれば写しを書き直す。merged は引かない。
	remote.states["issue:o/r#2"] = "closed"
	remote.calls = nil
	r.refreshRefs(context.Background(), now.Add(refstate.RefreshInterval-time.Second))
	r.refreshRefs(context.Background(), now.Add(refstate.RefreshInterval+time.Second))
	r.refreshRefs(context.Background(), now.Add(100*refstate.RefreshInterval))
	if want := []string{"issue:o/r#2"}; !reflect.DeepEqual(remote.calls, want) {
		t.Errorf("calls = %v, want %v", remote.calls, want)
	}
	if got := states(t, r)["issue:o/r#2"]; got != "closed" {
		t.Errorf("issue:o/r#2 の写し = %q", got)
	}
}

// gh が失敗したら写しを書かない (models/refstate の NoFalseComplete)。同じ理由はログに 1 回だけ書く。失敗した参照は
// RefreshInterval のあいだ引かず、ほかの参照を先に引く (失敗し続ける参照が引き直しを塞がない)。
func TestRefreshRefs_ghの失敗(t *testing.T) {
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	fail := errors.New("gh が見つからない")
	remote := &fakeRemote{states: map[string]string{"pr:o/r#2": "merged"}, errs: map[string]error{"pr:o/r#1": fail}}
	r, statusPath, add := newRefRuntime(t, remote, now)
	add("pr:o/r#1", "sid:a")
	add("pr:o/r#2", "sid:a")

	for i := range 4 {
		r.refreshRefs(context.Background(), now.Add(time.Duration(i)*time.Second))
	}
	if want := []string{"pr:o/r#1", "pr:o/r#2"}; !reflect.DeepEqual(remote.calls, want) {
		t.Errorf("calls = %v, want %v", remote.calls, want)
	}
	if got := states(t, r); !reflect.DeepEqual(got, map[string]string{"pr:o/r#2": "merged"}) {
		t.Errorf("写し = %v (失敗した参照を書いてはいけない)", got)
	}

	// 間隔が過ぎたら失敗した参照をまた引く。同じ理由ならログに書き直さない。
	r.refreshRefs(context.Background(), now.Add(refstate.RefreshInterval+time.Second))
	if len(remote.calls) != 3 {
		t.Errorf("calls = %v", remote.calls)
	}
	if n := strings.Count(readLog(t, statusPath), "gh が見つからない"); n != 1 {
		t.Errorf("失敗のログが %d 行 (1 行のはず)", n)
	}

	// 成功したら戻ったことをログに書き、写しを書く。
	delete(remote.errs, "pr:o/r#1")
	remote.states["pr:o/r#1"] = "open"
	r.refreshRefs(context.Background(), now.Add(2*refstate.RefreshInterval+time.Second))
	if got := states(t, r)["pr:o/r#1"]; got != "open" {
		t.Errorf("pr:o/r#1 の写し = %q", got)
	}
	if !strings.Contains(readLog(t, statusPath), "外部の参照の状態を引けるようになりました") {
		t.Errorf("戻ったことのログが無い: %q", readLog(t, statusPath))
	}
}

// 済んだ状態を写しに書いたときはログに 1 行残す (依存図の参照が完了に変わった理由を追えるようにする)。
func TestRefreshRefs_済んだことをログに書く(t *testing.T) {
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	remote := &fakeRemote{states: map[string]string{"pr:o/r#1": "merged"}}
	r, statusPath, add := newRefRuntime(t, remote, now)
	add("pr:o/r#1", "sid:a")
	r.refreshRefs(context.Background(), now)
	if !strings.Contains(readLog(t, statusPath), "pr:o/r#1 は merged") {
		t.Errorf("ログ = %q", readLog(t, statusPath))
	}
}

// 引く関数が無い (配線していない) runtime は何もしない。
func TestRefreshRefs_配線していない(t *testing.T) {
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	r, _ := newTestRuntime(t, &multiplexerfake.Client{}, now)
	r.refreshRefs(context.Background(), now)
}

// tick は辺の外部の参照を引き直す。
func TestDaemonRuntimeTick_refreshesRefs(t *testing.T) {
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	remote := &fakeRemote{states: map[string]string{"pr:o/r#1": "merged"}}
	r, _, add := newRefRuntime(t, remote, now)
	r.client = &multiplexerfake.Client{
		PaneListFunc: func(context.Context) ([]multiplexer.Pane, error) { return nil, nil },
	}
	add("pr:o/r#1", "sid:a")
	if _, _, err := r.tick(context.Background(), missingConfigPath(t), now); err != nil {
		t.Fatal(err)
	}
	if got := states(t, r)["pr:o/r#1"]; got != "merged" {
		t.Errorf("写し = %q", got)
	}
}
