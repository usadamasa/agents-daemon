package dag

import (
	"fmt"
	"reflect"
	"testing"
)

func less(a, b string) bool { return a < b }

// a → b → d、a → c、輪 x ⇄ y。
func graph() *Graph {
	return New([][2]string{{"a", "b"}, {"b", "d"}, {"a", "c"}, {"x", "y"}, {"y", "x"}}, less)
}

func TestDepthAndReach(t *testing.T) {
	g := graph()
	// 輪の中のノードは、輪を 1 周する手前までを数える (どちらから数えても同じ)。
	for key, want := range map[string]int{"a": 0, "b": 1, "c": 1, "d": 2, "x": 1, "y": 1} {
		if got := g.Depth(key); got != want {
			t.Errorf("Depth(%s) = %d, want %d", key, got, want)
		}
	}
	down, cyclic := g.Reach("a")
	if len(down) != 3 || cyclic {
		t.Errorf("Reach(a) = %v / %v", down, cyclic)
	}
	if down, cyclic := g.Reach("x"); len(down) != 1 || !cyclic {
		t.Errorf("Reach(x) = %v / %v, want 輪の中", down, cyclic)
	}
}

// 菱形を 60 段重ねた DAG (経路は 2^60 本) でも、段と最長の鎖をすぐに数え終える。
func TestLongestDiamonds(t *testing.T) {
	var links [][2]string
	key := func(i int, side string) string { return fmt.Sprintf("%03d%s", i, side) }
	for i := range 60 {
		for _, side := range []string{"a", "b"} {
			links = append(links, [2]string{key(i, ""), key(i, side)}, [2]string{key(i, side), key(i+1, "")})
		}
	}
	g := New(links, less)
	if got := len(g.Longest(key(0, ""))); got != 121 {
		t.Errorf("len(Longest) = %d, want 121", got)
	}
	if got := g.Depth(key(60, "")); got != 120 {
		t.Errorf("Depth = %d, want 120", got)
	}
}

func TestCauseAndLongest(t *testing.T) {
	g := New([][2]string{{"p", "z"}, {"q", "z"}, {"r", "q"}}, less)
	open := func(k string) bool { return k != "p" }
	rank := func(string) int { return 0 }
	if got := g.Cause("z", open, rank); !reflect.DeepEqual(got, []string{"q", "r"}) {
		t.Errorf("Cause(z) = %v", got)
	}
	if got := graph().Longest("a"); !reflect.DeepEqual(got, []string{"a", "b", "d"}) {
		t.Errorf("Longest(a) = %v", got)
	}
	if got := graph().Longest("x"); !reflect.DeepEqual(got, []string{"x", "y"}) {
		t.Errorf("Longest(x) = %v, want 輪で止まる", got)
	}
}
