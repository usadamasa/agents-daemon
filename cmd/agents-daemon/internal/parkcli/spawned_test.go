package parkcli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/usadamasa/agents-daemon/internal/spawnedsession"
)

func runSpawnedSession(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := newSpawnedSessionCmd()
	var out bytes.Buffer
	cmd.SetIn(strings.NewReader(""))
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

// `spawned-session mark <id>` は state に印を書き、書いた印を JSON で出す。spawn と peer-sessions skill の手順が
// 起こした session の中から呼ぶ (#557)。
func TestSpawnedSessionMark(t *testing.T) {
	root := parkTestEnv(t)
	const id = "session:01a1ca60-c767-7aaa-857b-7e167825e668"
	out, err := runSpawnedSession(t, "mark", id)
	if err != nil {
		t.Fatalf("mark error = %v", err)
	}
	var got spawnedsession.Record
	if err := json.Unmarshal([]byte(out), &got); err != nil || got.SessionID != id || got.CreatedAt.IsZero() {
		t.Errorf("stdout = %q (%v), want session_id %q と書いた時刻", out, err, id)
	}
	recs, err := spawnedsession.New(filepath.Join(root, "state", "agents-daemon")).List()
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 || recs[0].SessionID != id {
		t.Errorf("印 = %+v, want %q の 1 つ", recs, id)
	}
}

// session ID は 1 つだけ受ける。空の ID は書かない。
func TestSpawnedSessionMark_Args(t *testing.T) {
	parkTestEnv(t)
	for _, args := range [][]string{{"mark"}, {"mark", "a", "b"}, {"mark", ""}} {
		if _, err := runSpawnedSession(t, args...); err == nil {
			t.Errorf("%q: error = nil, want error", args)
		}
	}
}

// fakeCloser は close が呼ぶ多重化ツールの操作を記録する。goneAfterKill を立てると、kill の後の数え直しでは
// session が無いと返す (kill の直前に別の経路が session を閉じた競合を再現する)。
type fakeCloser struct {
	blocks        int
	found         bool
	countErr      error
	killErr       error
	goneAfterKill bool
	counted       []string
	killed        []string
}

func (f *fakeCloser) SessionBlocks(_ context.Context, id string) (int, bool, error) {
	f.counted = append(f.counted, id)
	if f.goneAfterKill && len(f.killed) > 0 {
		return 0, false, f.countErr
	}
	return f.blocks, f.found, f.countErr
}

func (f *fakeCloser) SessionKill(_ context.Context, id string) error {
	f.killed = append(f.killed, id)
	return f.killErr
}

// 印の無い session が多重化ツールに居れば閉じない。起こしていない session (自分や人の session) を閉じないための線で、
// 理由を出して止まる (#585)。
func TestCloseSpawnedSession_RefusesUnmarked(t *testing.T) {
	store := spawnedsession.New(t.TempDir())
	f := &fakeCloser{blocks: 1, found: true}
	_, err := closeSpawnedSession(context.Background(), store, f, "session:not-spawned")
	if err == nil || !strings.Contains(err.Error(), "印") {
		t.Errorf("error = %v, want 印が無いことを述べる error", err)
	}
	if len(f.killed) != 0 {
		t.Errorf("印の無い session を閉じた: killed=%v", f.killed)
	}
}

// 印が無くても多重化ツールに session が無ければ成功 (killed=false) にする。reaper が先に印を消した後でも、
// 印が残っているうちに打ったときと同じ返り方になる (#596)。
func TestCloseSpawnedSession_UnmarkedAlreadyGone(t *testing.T) {
	store := spawnedsession.New(t.TempDir())
	const id = "session:reaped"
	f := &fakeCloser{found: false}
	got, err := closeSpawnedSession(context.Background(), store, f, id)
	if err != nil {
		t.Fatalf("error = %v, want nil", err)
	}
	if got.SessionID != id || got.Killed {
		t.Errorf("result = %+v, want session_id %s と killed=false", got, id)
	}
	if len(f.killed) != 0 {
		t.Errorf("無い session を閉じようとした: %v", f.killed)
	}
}

// 印が無く、多重化ツールに session が有るかを確かめられないときは成功にしない。
func TestCloseSpawnedSession_UnmarkedCountErr(t *testing.T) {
	store := spawnedsession.New(t.TempDir())
	f := &fakeCloser{countErr: errors.New("socket")}
	if _, err := closeSpawnedSession(context.Background(), store, f, "session:unknown"); err == nil {
		t.Error("error = nil, want error")
	}
	if len(f.killed) != 0 {
		t.Errorf("killed = %v, want 空", f.killed)
	}
}

// 数えた後、kill の前に別の経路 (包みの c()) が session を閉じて kill が失敗しても、数え直して無ければ成功
// (killed=false) にして印を消す (#596)。
func TestCloseSpawnedSession_KillRaceGone(t *testing.T) {
	store := spawnedsession.New(t.TempDir())
	const id = "session:race"
	if err := store.Put(id); err != nil {
		t.Fatal(err)
	}
	f := &fakeCloser{blocks: 1, found: true, killErr: errors.New("not found"), goneAfterKill: true}
	got, err := closeSpawnedSession(context.Background(), store, f, id)
	if err != nil {
		t.Fatalf("error = %v, want nil", err)
	}
	if got.SessionID != id || got.Killed {
		t.Errorf("result = %+v, want session_id %s と killed=false", got, id)
	}
	if _, ok := store.Get(id); ok {
		t.Error("無くなった session の印が残った")
	}
}

// 印の付いた session は閉じて印を消す。label は起きた claude が rename するので、session_id で指す (#585)。
func TestCloseSpawnedSession_KillsMarked(t *testing.T) {
	store := spawnedsession.New(t.TempDir())
	const id = "session:01a1ca60-c767-7aaa-857b-7e167825e668"
	if err := store.Put(id); err != nil {
		t.Fatal(err)
	}
	f := &fakeCloser{blocks: 2, found: true}
	got, err := closeSpawnedSession(context.Background(), store, f, id)
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if len(f.killed) != 1 || f.killed[0] != id {
		t.Errorf("killed = %v, want [%s]", f.killed, id)
	}
	if got.SessionID != id || !got.Killed {
		t.Errorf("result = %+v, want session_id %s と killed=true", got, id)
	}
	if _, ok := store.Get(id); ok {
		t.Error("閉じた session の印が残った")
	}
}

// session がもう無ければ閉じずに印だけ消して成功にする。reaper と同じ扱いで、畳む手順を繰り返しても失敗しない。
func TestCloseSpawnedSession_AlreadyGone(t *testing.T) {
	store := spawnedsession.New(t.TempDir())
	const id = "session:gone"
	if err := store.Put(id); err != nil {
		t.Fatal(err)
	}
	f := &fakeCloser{found: false}
	got, err := closeSpawnedSession(context.Background(), store, f, id)
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if len(f.killed) != 0 {
		t.Errorf("無い session を閉じようとした: %v", f.killed)
	}
	if got.SessionID != id || got.Killed {
		t.Errorf("result = %+v, want session_id %s と killed=false", got, id)
	}
	if _, ok := store.Get(id); ok {
		t.Error("無くなった session の印が残った")
	}
}

// 数えられない、閉じられないときは error にして印を残す。残した印は daemon の reaper が後で拾う。
func TestCloseSpawnedSession_FailureKeepsMark(t *testing.T) {
	for name, f := range map[string]*fakeCloser{
		"数えられない": {countErr: errors.New("socket")},
		"閉じられない": {blocks: 1, found: true, killErr: errors.New("kill")},
	} {
		t.Run(name, func(t *testing.T) {
			store := spawnedsession.New(t.TempDir())
			const id = "session:x"
			if err := store.Put(id); err != nil {
				t.Fatal(err)
			}
			if _, err := closeSpawnedSession(context.Background(), store, f, id); err == nil {
				t.Error("error = nil, want error")
			}
			if _, ok := store.Get(id); !ok {
				t.Error("失敗したのに印を消した")
			}
		})
	}
}

// `spawned-session close` は印の無い session を、rex で無いと確かめられなければ成功にしない。
func TestSpawnedSessionClose_UnmarkedUnreachable(t *testing.T) {
	parkTestEnv(t)
	t.Setenv("REX_BIN_PATH", filepath.Join(t.TempDir(), "rex-missing"))
	if _, err := runSpawnedSession(t, "close", "session:not-spawned"); err == nil || !strings.Contains(err.Error(), "印") {
		t.Errorf("error = %v, want 印が無いことを述べる error", err)
	}
}

// session ID は 1 つだけ受ける。空の ID は受けない。
func TestSpawnedSessionClose_Args(t *testing.T) {
	parkTestEnv(t)
	for _, args := range [][]string{{"close"}, {"close", "a", "b"}, {"close", ""}} {
		if _, err := runSpawnedSession(t, args...); err == nil {
			t.Errorf("%q: error = nil, want error", args)
		}
	}
}
