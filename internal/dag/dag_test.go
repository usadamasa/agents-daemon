package dag

import (
	"fmt"
	"reflect"
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

// draw は Rows の結果を目で読める形にする: ノードの行は lanes の後にノード、つなぐだけの行は lanes だけ。
func draw(rows []Row) string {
	lines := make([]string, 0, len(rows))
	for _, r := range rows {
		if r.Node == "" {
			lines = append(lines, r.Lanes)
			continue
		}
		lines = append(lines, r.Lanes+" "+r.Node)
	}
	return strings.Join(lines, "\n")
}

func solid(string, string) bool { return false }

func TestRows(t *testing.T) {
	tests := []struct {
		name   string
		links  [][2]string
		order  []string
		dashed func(before, after string) bool
		want   string
	}{
		{
			name:  "鎖は 1 本のレーン",
			links: [][2]string{{"a", "b"}, {"b", "c"}},
			order: []string{"a", "b", "c"},
			want:  "* a\n* b\n* c",
		},
		{
			name:  "合流は上流のレーンをいちばん左に寄せる行を挟む",
			links: [][2]string{{"a", "c"}, {"b", "c"}},
			order: []string{"a", "b", "c"},
			want:  "* a\n│ * b\n├─┘\n* c",
		},
		{
			name:  "分岐は下流の分だけ右にレーンを開く行を挟み、閉じたレーンは空きのまま残す",
			links: [][2]string{{"a", "b"}, {"a", "c"}},
			order: []string{"a", "b", "c"},
			want:  "* a\n├─┐\n* │ b\n  * c",
		},
		{
			name:  "3 本に分かれる",
			links: [][2]string{{"a", "b"}, {"a", "c"}, {"a", "d"}},
			order: []string{"a", "b", "c", "d"},
			want:  "* a\n├─┬─┐\n* │ │ b\n  * │ c\n    * d",
		},
		{
			name:  "合流の間を通り抜けるレーンとは ┼ で交差する",
			links: [][2]string{{"a", "d"}, {"b", "e"}, {"c", "d"}, {"f", "e"}},
			order: []string{"a", "b", "c", "d", "f", "e"},
			want:  "* a\n│ * b\n│ │ * c\n├─┼─┘\n* │ d\n* │ f\n├─┘\n* e",
		},
		{
			name:   "dashed の辺は縦を ┆、横を ┄ で描く。辺の無いノードは空きのレーンに出る",
			links:  [][2]string{{"a", "b"}, {"c", "b"}},
			order:  []string{"a", "x", "c", "b"},
			dashed: func(_, after string) bool { return after == "b" },
			want:   "* a\n┆ * x\n┆ * c\n├┄┘\n* b",
		},
		{
			name:  "直前のノードの下流で上流が出揃ったものを、order より先に出す",
			links: [][2]string{{"a", "b"}, {"c", "d"}},
			order: []string{"a", "c", "b", "d"},
			want:  "* a\n* b\n* c\n* d",
		},
		{
			name:  "閉じて空いたレーンを次のノードが使い、右端の空きは詰める",
			links: [][2]string{{"a", "b"}, {"a", "c"}, {"c", "d"}, {"f", "d"}},
			order: []string{"a", "b", "c", "f", "d", "g"},
			want:  "* a\n├─┐\n* │ b\n  * c\n* │ f\n├─┘\n* d\n* g",
		},
		{
			name:  "待ちの輪は Depth の小さいものから出し、戻る辺は描かない。ノードは 1 回だけ出る",
			links: [][2]string{{"x", "y"}, {"y", "x"}, {"y", "z"}},
			order: []string{"x", "y", "z"},
			want:  "* x\n* y\n* z",
		},
		{
			name:  "辺の無いノードだけなら 1 本ずつ",
			links: nil,
			order: []string{"p", "q"},
			want:  "* p\n* q",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dashed := tt.dashed
			if dashed == nil {
				dashed = solid
			}
			got := draw(New(tt.links, less).Rows(tt.order, dashed))
			if got != tt.want {
				t.Errorf("Rows =\n%s\nwant\n%s", got, tt.want)
			}
		})
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
