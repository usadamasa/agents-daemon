package dag

import (
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
