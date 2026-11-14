package edge

import (
	"os"
	"path/filepath"
	"testing"
)

func mustRemove(t *testing.T, s Store, before, after Endpoint) {
	t.Helper()
	removed, err := s.Remove(before, after)
	if err != nil {
		t.Fatalf("Remove() error = %v", err)
	}
	if !removed.Removed {
		t.Fatalf("%s → %s を消せなかった", before.Ref, after.Ref)
	}
}

// declinedOf は組 (before、after) の「導かない」の記録のファイルの情報。無ければ ok が false。
func declinedOf(t *testing.T, s Store, before, after string) (os.FileInfo, bool) {
	t.Helper()
	info, err := os.Stat(filepath.Join(s.Dir, edgeID(before, after)+".declined"))
	if err != nil {
		return nil, false
	}
	return info, true
}

// 人が sid → pr を `edge rm` (アプリの Unlink も) で消したら、その組は導き直さない (#576、models/prlink の NoRederive)。
// 導いた辺でも人が書いた辺でも同じ。
func TestAnchor_人が消した組には導き直さない(t *testing.T) {
	for _, derived := range []bool{true, false} {
		s := New(t.TempDir())
		mustAdd(t, s, Endpoint{Ref: "pr:o/r#1", CWD: "/h"}, Endpoint{Ref: "task:H/1", CWD: "/h"}, "", testNow)
		if derived {
			if _, ok := mustAnchor(t, s, "A", "/a", "pr:o/r#1"); !ok {
				t.Fatal("導かなかった")
			}
		} else {
			mustAdd(t, s, Endpoint{Ref: "sid:A", CWD: "/a"}, Endpoint{Ref: "pr:o/r#1", CWD: "/h"}, "", testNow)
		}
		mustRemove(t, s, Endpoint{Ref: "sid:A", CWD: "/a"}, Endpoint{Ref: "pr:o/r#1", CWD: "/h"})

		if _, ok := mustAnchor(t, s, "A", "/a", "pr:o/r#1"); ok {
			t.Errorf("人が消した組を導き直した (derived %v)", derived)
		}
		if _, found := refsOf(t, s)[[2]string{"sid:A", "pr:o/r#1"}]; found {
			t.Errorf("消した辺が残っている (derived %v)", derived)
		}
	}
}

// 「導かない」は組ごと。ほかのセッションやほかの PR には導く。sid → pr でない辺を消しても記録しない。
func TestAnchor_導かないの記録は組ごと(t *testing.T) {
	s := New(t.TempDir())
	mustAdd(t, s, Endpoint{Ref: "pr:o/r#1", CWD: "/h"}, Endpoint{Ref: "task:H/1", CWD: "/h"}, "", testNow)
	mustAdd(t, s, Endpoint{Ref: "pr:o/r#2", CWD: "/h"}, Endpoint{Ref: "task:H/1", CWD: "/h"}, "", testNow)
	if _, ok := mustAnchor(t, s, "A", "/a", "pr:o/r#1"); !ok {
		t.Fatal("導かなかった")
	}
	mustRemove(t, s, Endpoint{Ref: "sid:A", CWD: "/a"}, Endpoint{Ref: "pr:o/r#1", CWD: "/h"})

	if _, ok := mustAnchor(t, s, "A", "/a", "pr:o/r#2"); !ok {
		t.Error("別の PR に導かなかった")
	}
	if _, ok := mustAnchor(t, s, "B", "/b", "pr:o/r#1"); !ok {
		t.Error("別のセッションに導かなかった")
	}

	mustRemove(t, s, Endpoint{Ref: "pr:o/r#2", CWD: "/h"}, Endpoint{Ref: "task:H/1", CWD: "/h"})
	if _, found := declinedOf(t, s, "pr:o/r#2", "task:H/1"); found {
		t.Error("sid → pr でない辺の削除を記録した")
	}
}

// 人が同じ組を `edge add` で書き直したら、「導かない」の記録を外す。
func TestAdd_人が書き直したら導かないの記録を外す(t *testing.T) {
	s := New(t.TempDir())
	mustAdd(t, s, Endpoint{Ref: "pr:o/r#1", CWD: "/h"}, Endpoint{Ref: "task:H/1", CWD: "/h"}, "", testNow)
	if _, ok := mustAnchor(t, s, "A", "/a", "pr:o/r#1"); !ok {
		t.Fatal("導かなかった")
	}
	mustRemove(t, s, Endpoint{Ref: "sid:A", CWD: "/a"}, Endpoint{Ref: "pr:o/r#1", CWD: "/h"})
	if _, found := declinedOf(t, s, "sid:A", "pr:o/r#1"); !found {
		t.Fatal("消した組を記録していない")
	}

	mustAdd(t, s, Endpoint{Ref: "sid:A", CWD: "/a"}, Endpoint{Ref: "pr:o/r#1", CWD: "/h"}, "", testNow)
	if _, found := declinedOf(t, s, "sid:A", "pr:o/r#1"); found {
		t.Error("書き直した組の記録が残っている")
	}
}

// 記録は保持期間 (Retention) で Prune が消す。導きが記録で飛ばした回に mtime を進めるので、導く条件が揃っている間は
// 消えない (models/prlink の TouchDecline)。
func TestPrune_導かないの記録は飛ばした回から保持期間で消える(t *testing.T) {
	s := New(t.TempDir())
	mustAdd(t, s, Endpoint{Ref: "pr:o/r#1", CWD: "/h"}, Endpoint{Ref: "task:H/1", CWD: "/h"}, "", testNow)
	if _, ok := mustAnchor(t, s, "A", "/a", "pr:o/r#1"); !ok {
		t.Fatal("導かなかった")
	}
	mustRemove(t, s, Endpoint{Ref: "sid:A", CWD: "/a"}, Endpoint{Ref: "pr:o/r#1", CWD: "/h"})
	old := testNow.Add(-Retention - 1)
	if err := os.Chtimes(filepath.Join(s.Dir, edgeID("sid:A", "pr:o/r#1")+".declined"), old, old); err != nil {
		t.Fatal(err)
	}
	// 辺の側は消えないよう、今の時刻に揃える。
	if err := s.TouchBefore(map[string]bool{"pr:o/r#1": true}, testNow); err != nil {
		t.Fatal(err)
	}

	if _, ok := mustAnchor(t, s, "A", "/a", "pr:o/r#1"); ok {
		t.Fatal("人が消した組を導き直した")
	}
	info, found := declinedOf(t, s, "sid:A", "pr:o/r#1")
	if !found || !info.ModTime().Equal(testNow) {
		t.Fatalf("導きを飛ばした回に記録の mtime を進めていない: %v (found %v)", info, found)
	}
	if n, err := s.Prune(testNow); err != nil || n != 0 {
		t.Errorf("Prune() = %d, %v; 飛ばした直後の記録を消した", n, err)
	}

	later := testNow.Add(Retention + 1)
	if _, err := s.Prune(later); err != nil {
		t.Fatal(err)
	}
	if _, found := declinedOf(t, s, "sid:A", "pr:o/r#1"); found {
		t.Error("保持期間を過ぎた記録が残っている")
	}
}
