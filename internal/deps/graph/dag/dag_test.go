package dag

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
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

func fold(before, after string, via ...string) Folded {
	return Folded{Before: before, After: after, Via: via}
}

// Fold は隠したノード (大文字) を通り抜ける経路を、見えているノードどうしの 1 本の辺に畳む。
func TestFold(t *testing.T) {
	shown := func(k string) bool { return strings.ToLower(k) == k }
	cases := []struct {
		name  string
		links [][2]string
		want  []Folded
	}{
		{"鎖は通り抜けたノードを経路の順に持つ", [][2]string{{"a", "B"}, {"B", "C"}, {"C", "d"}},
			[]Folded{{Before: "a", After: "d", Via: []string{"B", "C"}}}},
		{"隠したノードで分かれて合わさる先は見えている組ごとに 1 本", [][2]string{{"a", "H"}, {"b", "H"}, {"H", "c"}, {"H", "d"}},
			[]Folded{fold("a", "c", "H"), fold("a", "d", "H"), fold("b", "c", "H"), fold("b", "d", "H")}},
		{"直接の辺がある組は畳まない", [][2]string{{"a", "H"}, {"H", "c"}, {"a", "c"}}, nil},
		{"見えているノードの先へは通り抜けない", [][2]string{{"a", "b"}, {"b", "H"}, {"H", "c"}},
			[]Folded{fold("b", "c", "H")}},
		{"同じ組へ 2 本の経路があっても 1 本", [][2]string{{"a", "H"}, {"a", "K"}, {"H", "c"}, {"K", "c"}},
			[]Folded{fold("a", "c", "H")}},
		{"隠したノードの輪でも止まる", [][2]string{{"a", "H"}, {"H", "K"}, {"K", "H"}, {"K", "c"}},
			[]Folded{fold("a", "c", "H", "K")}},
		{"隠したノードで行き止まりなら辺は無い", [][2]string{{"a", "H"}}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var order []string
			for _, l := range c.links {
				for _, k := range l {
					if !slices.Contains(order, k) {
						order = append(order, k)
					}
				}
			}
			slices.Sort(order)
			got := New(c.links, less).Fold(order, shown)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("Fold = %v, want %v", got, c.want)
			}
		})
	}
}
