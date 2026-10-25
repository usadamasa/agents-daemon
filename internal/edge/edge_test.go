package edge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var testNow = time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)

func mustAdd(t *testing.T, s Store, before, after Endpoint, note string, now time.Time) Added {
	t.Helper()
	added, err := s.Add(before, after, note, now)
	if err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	return added
}

// 辺は edges/<id>.json に 1 本 1 ファイルで書かれ、端点の repo は cwd の repo の鍵になる。
func TestAdd_writesOneFilePerEdge(t *testing.T) {
	s := New(t.TempDir())
	added := mustAdd(t, s, Endpoint{Ref: "session:ad-134", CWD: "/home/a"}, Endpoint{Ref: "issue:usadamasa/agents-daemon#135", CWD: "/home/b"}, "段 4 の後", testNow)

	e := added.Edge
	if !added.Created || e.Before.Ref != "session:ad-134" || e.Before.Repo != "/home/a" || e.Before.CWD != "/home/a" || e.After.Repo != "/home/b" || e.Note != "段 4 の後" || !e.CreatedAt.Equal(testNow) {
		t.Errorf("added = %+v", added)
	}
	if _, err := os.Stat(filepath.Join(s.Dir, e.ID+".json")); err != nil {
		t.Errorf("ファイルが無い: %v", err)
	}
}

// 同じ辺 (before と after の参照が同じ) の二重登録は同じファイルを書き直すだけで、辺は増えない。
// created_at は最初の登録のまま、note と repo は後から書いた側になる。
func TestAdd_sameEdgeIsIdempotent(t *testing.T) {
	s := New(t.TempDir())
	first := mustAdd(t, s, Endpoint{Ref: "session:a", CWD: "/home/a"}, Endpoint{Ref: "session:b", CWD: "/home/b"}, "", testNow)
	later := testNow.Add(time.Hour)
	second := mustAdd(t, s, Endpoint{Ref: " session:a ", CWD: "/home/a"}, Endpoint{Ref: "session:b", CWD: "/home/c"}, "補足", later)

	if second.Created || second.Edge.ID != first.Edge.ID {
		t.Fatalf("別の辺になった: %+v", second)
	}
	if !second.Edge.CreatedAt.Equal(testNow) || second.Edge.Note != "補足" || second.Edge.After.Repo != "/home/c" {
		t.Errorf("edge = %+v", second.Edge)
	}
	if got, _ := s.Find("", ""); len(got) != 1 {
		t.Errorf("辺の数 = %d, want 1", len(got))
	}
	// 向きが逆なら別の辺。
	if reversed := mustAdd(t, s, Endpoint{Ref: "session:b", CWD: "/home/b"}, Endpoint{Ref: "session:a", CWD: "/home/a"}, "", testNow); !reversed.Created {
		t.Errorf("逆向きの辺がまとまった")
	}
}

func TestAdd_invalidRefs(t *testing.T) {
	s := New(t.TempDir())
	ok := Endpoint{Ref: "session:a", CWD: "/home/a"}
	for name, sp := range map[string]Endpoint{
		"種類が無い":           {Ref: "a", CWD: "/home/a"},
		"種類が範囲外":          {Ref: "branch:main", CWD: "/home/a"},
		"値が空":             {Ref: "session:", CWD: "/home/a"},
		"セッション名に空白":       {Ref: "session:a b", CWD: "/home/a"},
		"issue に番号が無い":    {Ref: "issue:usadamasa/agents-daemon", CWD: "/home/a"},
		"pr の repo が無い":   {Ref: "pr:#12", CWD: "/home/a"},
		"task に task が無い": {Ref: "task:s1", CWD: "/home/a"},
		"todo は端点にしない":    {Ref: "todo:s1/0", CWD: "/home/a"},
		"cwd が空":          {Ref: "session:b", CWD: ""},
		"cwd が相対":         {Ref: "session:b", CWD: "home/a"},
	} {
		if _, err := s.Add(ok, sp, "", testNow); err == nil {
			t.Errorf("%s: Add が通った", name)
		}
	}
	if _, err := s.Add(ok, Endpoint{Ref: "session:a", CWD: "/home/b"}, "", testNow); err == nil {
		t.Errorf("自分自身への辺が通った")
	}
	for _, ref := range []string{"session:ad-134-edges", "issue:usadamasa/agents-daemon#134", "pr:usadamasa/agents-config.x#7", "task:2e0b-11/3", "sid:2e0b-11"} {
		if _, err := s.Add(ok, Endpoint{Ref: ref, CWD: "/home/b"}, "", testNow); err != nil {
			t.Errorf("%s: %v", ref, err)
		}
	}
}

// Remove は同じ両端の辺を消す。無い辺を消しても失敗しない (二重に消しても害が無い)。
func TestRemove(t *testing.T) {
	s := New(t.TempDir())
	before, after := Endpoint{Ref: "session:a", CWD: "/home/a"}, Endpoint{Ref: "session:b", CWD: "/home/b"}
	added := mustAdd(t, s, before, after, "", testNow)

	removed, err := s.Remove(before, after)
	if err != nil || !removed.Removed || removed.ID != added.Edge.ID {
		t.Fatalf("Remove() = %+v, %v", removed, err)
	}
	if got, _ := s.Find("", ""); len(got) != 0 {
		t.Errorf("消えていない: %+v", got)
	}
	again, err := s.Remove(before, after)
	if err != nil || again.Removed {
		t.Errorf("2 回目の Remove() = %+v, %v", again, err)
	}
}

// Find は repo (どちらかの端点) と参照 (どちらかの端点) で絞り、登録の順に返す。該当なしは空の配列。
func TestFind(t *testing.T) {
	s := New(t.TempDir())
	ab := mustAdd(t, s, Endpoint{Ref: "session:a", CWD: "/home/a"}, Endpoint{Ref: "session:b", CWD: "/home/b"}, "", testNow).Edge
	bc := mustAdd(t, s, Endpoint{Ref: "session:b", CWD: "/home/b"}, Endpoint{Ref: "session:c", CWD: "/home/c"}, "", testNow.Add(time.Minute)).Edge

	cases := []struct {
		name, repo, ref string
		want            []string
	}{
		{"絞らない", "", "", []string{ab.ID, bc.ID}},
		{"repo が before", "/home/a", "", []string{ab.ID}},
		{"repo が after", "/home/c", "", []string{bc.ID}},
		{"参照が両方の辺に出る", "", "session:b", []string{ab.ID, bc.ID}},
		{"repo と参照の両方", "/home/a", "session:c", []string{}},
	}
	for _, c := range cases {
		got, err := s.Find(c.repo, c.ref)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got == nil {
			t.Errorf("%s: 該当なしでも空の配列を返す", c.name)
		}
		ids := []string{}
		for _, e := range got {
			ids = append(ids, e.ID)
		}
		if strings.Join(ids, ",") != strings.Join(c.want, ",") {
			t.Errorf("%s: %v, want %v", c.name, ids, c.want)
		}
	}
	if got, err := (Store{}).Find("", ""); err != nil || len(got) != 0 {
		t.Errorf("ゼロ値の Find() = %+v, %v", got, err)
	}
}
