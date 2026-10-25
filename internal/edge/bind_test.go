package edge

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// pickFrom は名前 → session ID の表で選ぶ Pick。選んだ辺の created_at を seen に残す。
func pickFrom(names map[string]string, seen *[]time.Time) Pick {
	return func(name string, createdAt time.Time) (string, bool) {
		if seen != nil {
			*seen = append(*seen, createdAt)
		}
		id, ok := names[name]
		return id, ok
	}
}

func files(t *testing.T, s Store) []string {
	t.Helper()
	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		if isEdgeFile(e) {
			names = append(names, e.Name())
		}
	}
	return names
}

// Bind は session:<名前> の端点を sid:<id> に書き換え、元の参照を bound_from に残す。id とファイルは変わらない。
// Pick には辺を書いた時刻を渡す。選べない名前の端点はそのまま。
func TestBind(t *testing.T) {
	s := New(t.TempDir())
	added := mustAdd(t, s, Endpoint{Ref: "session:a", CWD: "/home/a"}, Endpoint{Ref: "session:later", CWD: "/home/b"}, "後", testNow)
	var seen []time.Time
	bound, err := s.Bind(pickFrom(map[string]string{"a": "S1"}, &seen))
	if err != nil || len(bound) != 1 {
		t.Fatalf("Bind() = %+v, %v", bound, err)
	}
	got, _ := s.Find("", "")
	if len(got) != 1 {
		t.Fatalf("辺 = %+v", got)
	}
	e := got[0]
	if e.ID != added.Edge.ID || e.Before.Ref != "sid:S1" || e.Before.BoundFrom != "session:a" || e.Before.CWD != "/home/a" ||
		e.After.Ref != "session:later" || e.After.BoundFrom != "" || e.Note != "後" || !e.CreatedAt.Equal(testNow) {
		t.Errorf("結びついた辺 = %+v", e)
	}
	if len(seen) == 0 || !seen[0].Equal(testNow) {
		t.Errorf("Pick に渡した created_at = %v", seen)
	}
	if names := files(t, s); len(names) != 1 || names[0] != added.Edge.ID+".json" {
		t.Errorf("ファイル = %v", names)
	}
}

// 結びついた端点は結び直さない。後から同じ名前の別のセッションが選ばれても変わらない。
func TestBind_once(t *testing.T) {
	s := New(t.TempDir())
	mustAdd(t, s, Endpoint{Ref: "session:a", CWD: "/home/a"}, Endpoint{Ref: "issue:o/r#1", CWD: "/home/b"}, "", testNow)
	if _, err := s.Bind(pickFrom(map[string]string{"a": "S1"}, nil)); err != nil {
		t.Fatal(err)
	}
	bound, err := s.Bind(pickFrom(map[string]string{"a": "S2"}, nil))
	if err != nil || len(bound) != 0 {
		t.Fatalf("2 回目の Bind() = %+v, %v", bound, err)
	}
	if got, _ := s.Find("", ""); got[0].Before.Ref != "sid:S1" {
		t.Errorf("結び直した: %+v", got[0].Before)
	}
}

// 結びついた後に同じ参照の組で edge add しても、結びついた端点は名前に戻らず、辺は増えない。
// 結びついた後の参照の組 (sid:) で edge add しても同じ辺に当たる。
func TestAdd_afterBind(t *testing.T) {
	s := New(t.TempDir())
	before, after := Endpoint{Ref: "session:a", CWD: "/home/a"}, Endpoint{Ref: "session:b", CWD: "/home/b"}
	mustAdd(t, s, before, after, "", testNow)
	if _, err := s.Bind(pickFrom(map[string]string{"a": "S1"}, nil)); err != nil {
		t.Fatal(err)
	}
	again := mustAdd(t, s, before, Endpoint{Ref: "session:b", CWD: "/home/c"}, "補足", testNow.Add(time.Hour))
	if again.Created || again.Edge.Before.Ref != "sid:S1" || again.Edge.Before.BoundFrom != "session:a" || again.Edge.After.CWD != "/home/c" ||
		again.Edge.Note != "補足" || !again.Edge.CreatedAt.Equal(testNow) {
		t.Errorf("同じ参照の組の Add = %+v", again)
	}
	byBound := mustAdd(t, s, Endpoint{Ref: "sid:S1", CWD: "/home/a"}, after, "", testNow.Add(2*time.Hour))
	if byBound.Created || byBound.Edge.ID != again.Edge.ID || byBound.Edge.Before.BoundFrom != "session:a" {
		t.Errorf("結びついた後の参照の組の Add = %+v", byBound)
	}
	if names := files(t, s); len(names) != 1 {
		t.Errorf("辺が増えた: %v", names)
	}
	// 書く側から bound_from は受け取らない。
	forged := mustAdd(t, s, Endpoint{Ref: "issue:o/r#2", CWD: "/home/a", BoundFrom: "session:x"}, after, "", testNow)
	if forged.Edge.Before.BoundFrom != "" {
		t.Errorf("bound_from を受け取った: %+v", forged.Edge.Before)
	}
}

// edge rm は書いたときの参照の組でも、結びついた後の参照の組でも同じ辺を消す。
func TestRemove_afterBind(t *testing.T) {
	for _, c := range []struct {
		name   string
		before string
	}{
		{"書いたときの参照", "session:a"},
		{"結びついた後の参照", "sid:S1"},
	} {
		s := New(t.TempDir())
		added := mustAdd(t, s, Endpoint{Ref: "session:a", CWD: "/home/a"}, Endpoint{Ref: "session:b", CWD: "/home/b"}, "", testNow)
		if _, err := s.Bind(pickFrom(map[string]string{"a": "S1"}, nil)); err != nil {
			t.Fatal(err)
		}
		removed, err := s.Remove(Endpoint{Ref: c.before, CWD: "/home/a"}, Endpoint{Ref: "session:b", CWD: "/home/b"})
		if err != nil || !removed.Removed || removed.ID != added.Edge.ID {
			t.Errorf("%s: Remove() = %+v, %v", c.name, removed, err)
		}
		if got, _ := s.Find("", ""); len(got) != 0 {
			t.Errorf("%s: 残った: %+v", c.name, got)
		}
	}
}

// 結びつけると今の参照の組が既にある辺と重なるときは、同じ依存なので結びつける側を消して 1 本にする。
func TestBind_mergesSamePair(t *testing.T) {
	s := New(t.TempDir())
	kept := mustAdd(t, s, Endpoint{Ref: "sid:S1", CWD: "/home/a"}, Endpoint{Ref: "issue:o/r#1", CWD: "/home/b"}, "", testNow).Edge
	mustAdd(t, s, Endpoint{Ref: "session:a", CWD: "/home/a"}, Endpoint{Ref: "issue:o/r#1", CWD: "/home/b"}, "", testNow)
	bound, err := s.Bind(pickFrom(map[string]string{"a": "S1"}, nil))
	if err != nil || len(bound) != 0 {
		t.Fatalf("Bind() = %+v, %v", bound, err)
	}
	if got, _ := s.Find("", ""); len(got) != 1 || got[0].ID != kept.ID {
		t.Errorf("辺 = %+v, want %s だけ", got, kept.ID)
	}
}

// 両端が同じセッションになる結びつけはしない (自分自身への辺を作らない)。
func TestBind_noSelfEdge(t *testing.T) {
	s := New(t.TempDir())
	mustAdd(t, s, Endpoint{Ref: "sid:S1", CWD: "/home/a"}, Endpoint{Ref: "session:a", CWD: "/home/b"}, "", testNow)
	if bound, err := s.Bind(pickFrom(map[string]string{"a": "S1"}, nil)); err != nil || len(bound) != 0 {
		t.Fatalf("Bind() = %+v, %v", bound, err)
	}
	if got, _ := s.Find("", ""); got[0].After.Ref != "session:a" {
		t.Errorf("自分自身への辺にした: %+v", got[0])
	}
}

// 置き場が無い・ゼロ値の Store では何もしない。
func TestBind_noDir(t *testing.T) {
	for _, s := range []Store{{}, New(filepath.Join(t.TempDir(), "missing"))} {
		if bound, err := s.Bind(pickFrom(nil, nil)); err != nil || len(bound) != 0 {
			t.Errorf("Bind() = %+v, %v", bound, err)
		}
		if s.Dir != "" {
			if _, err := os.Stat(s.Dir); !os.IsNotExist(err) {
				t.Errorf("置き場を作った: %v", err)
			}
		}
	}
}
