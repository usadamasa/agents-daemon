package dag

// Graph の数え方が満たすべき性質を、乱択のグラフ (輪を含む) で確かめる。期待値は総当たりの参照実装から取る。
// 辺は edge store が書くものと同じ範囲 (自己辺なし、同じ辺の重複なし) で引く。

import (
	"fmt"
	"math/rand/v2"
	"reflect"
	"slices"
	"strings"
	"testing"
)

const graphCount = 2000

// randomGraph は最大 7 ノード、最大 12 本の辺を引く。
func randomGraph(r *rand.Rand) (links [][2]string, nodes []string) {
	n := 1 + r.IntN(7)
	for i := range n {
		nodes = append(nodes, string(rune('a'+i)))
	}
	seen := map[[2]string]bool{}
	for range r.IntN(13) {
		l := [2]string{nodes[r.IntN(n)], nodes[r.IntN(n)]}
		if l[0] == l[1] || seen[l] {
			continue
		}
		seen[l] = true
		links = append(links, l)
	}
	return links, nodes
}

// ref は総当たりの参照実装。
type ref struct {
	preds, succs map[string][]string
}

func newRef(links [][2]string) ref {
	rf := ref{preds: map[string][]string{}, succs: map[string][]string{}}
	for _, l := range links {
		rf.succs[l[0]] = append(rf.succs[l[0]], l[1])
		rf.preds[l[1]] = append(rf.preds[l[1]], l[0])
	}
	return rf
}

// acyclic は辺に輪が無いか。
func (rf ref) acyclic(nodes []string) bool {
	for _, k := range nodes {
		if _, back := rf.reach(k); back {
			return false
		}
	}
	return true
}

// reach は key から下流へたどれるノードと、key へ戻るか。
func (rf ref) reach(key string) (map[string]bool, bool) {
	seen, back := map[string]bool{}, false
	queue := slices.Clone(rf.succs[key])
	for len(queue) > 0 {
		k := queue[0]
		queue = queue[1:]
		if k == key {
			back = true
		}
		if seen[k] {
			continue
		}
		seen[k] = true
		queue = append(queue, rf.succs[k]...)
	}
	delete(seen, key)
	return seen, back
}

// upstream は輪の無いグラフで key へ至る最長の経路の辺の数。
func (rf ref) upstream(key string) int {
	d := 0
	for _, p := range rf.preds[key] {
		d = max(d, rf.upstream(p)+1)
	}
	return d
}

// downstream は輪の無いグラフで key から下る最長の鎖のノード数 (key を含む)。
func (rf ref) downstream(key string) int {
	n := 1
	for _, s := range rf.succs[key] {
		n = max(n, rf.downstream(s)+1)
	}
	return n
}

func isEdge(links [][2]string, before, after string) bool {
	return slices.Contains(links, [2]string{before, after})
}

func TestGraph_乱択のグラフで参照実装と一致する(t *testing.T) {
	for seed := range graphCount {
		r := rand.New(rand.NewPCG(uint64(seed), 0)) // #nosec G115 -- seed は非負
		links, nodes := randomGraph(r)
		rf := newRef(links)
		shared := New(links, less)
		order := slices.Clone(nodes)
		r.Shuffle(len(order), func(i, j int) { order[i], order[j] = order[j], order[i] })
		fail := func(key, msg string) {
			t.Fatalf("seed %d links=%v key=%s: %s", seed, links, key, msg)
		}
		for _, key := range order {
			checkDepthAndLongest(t, fail, links, rf, shared, key, nodes)
			reach, cyclic := shared.Reach(key)
			if wantReach, wantCyclic := rf.reach(key); !reflect.DeepEqual(reach, wantReach) || cyclic != wantCyclic {
				fail(key, fmt.Sprintf("Reach = %v/%v, want %v/%v", reach, cyclic, wantReach, wantCyclic))
			}
			checkCause(fail, r, links, shared, key)
		}
		checkRows(fail, r, shared, nodes)
	}
}

// checkDepthAndLongest は Depth と Longest が問い合わせの順に依らず、輪が無ければ総当たりと一致し、
// Longest が辺をたどる重複の無い鎖であることを見る。
func checkDepthAndLongest(t *testing.T, fail func(key, msg string), links [][2]string, rf ref, shared *Graph, key string, nodes []string) {
	t.Helper()
	fresh := New(links, less)
	depth, longest := shared.Depth(key), shared.Longest(key)
	if d := fresh.Depth(key); d != depth {
		fail(key, fmt.Sprintf("Depth が問い合わせの順で変わる: shared %d, fresh %d", depth, d))
	}
	if l := fresh.Longest(key); !reflect.DeepEqual(l, longest) {
		fail(key, fmt.Sprintf("Longest が問い合わせの順で変わる: shared %v, fresh %v", longest, l))
	}
	if len(longest) == 0 || longest[0] != key {
		fail(key, fmt.Sprintf("Longest %v が key から始まらない", longest))
	}
	for i := 1; i < len(longest); i++ {
		if !isEdge(links, longest[i-1], longest[i]) {
			fail(key, fmt.Sprintf("Longest %v の %s → %s が辺でない", longest, longest[i-1], longest[i]))
		}
	}
	if uniq := slices.Compact(slices.Sorted(slices.Values(longest))); len(uniq) != len(longest) {
		fail(key, fmt.Sprintf("Longest %v に同じノードが 2 回出る", longest))
	}
	if rf.acyclic(nodes) {
		if want := rf.upstream(key); depth != want {
			fail(key, fmt.Sprintf("Depth = %d, want %d (総当たり)", depth, want))
		}
		if want := rf.downstream(key); len(longest) != want {
			fail(key, fmt.Sprintf("len(Longest) = %d, want %d (総当たり)", len(longest), want))
		}
	}
}

// checkCause は Cause が open な上流だけを、辺をさかのぼって重複なく並べることを見る。
func checkCause(fail func(key, msg string), r *rand.Rand, links [][2]string, g *Graph, key string) {
	openSet := map[string]bool{}
	ranks := map[string]int{}
	for _, l := range links {
		for _, k := range l {
			openSet[k] = r.IntN(3) != 0
			ranks[k] = r.IntN(3)
		}
	}
	chain := g.Cause(key, func(k string) bool { return openSet[k] }, func(k string) int { return ranks[k] })
	prev := key
	for _, k := range chain {
		if !openSet[k] || !isEdge(links, k, prev) || k == key {
			fail(key, fmt.Sprintf("Cause %v: %s が open な %s の上流でない", chain, k, prev))
		}
		prev = k
	}
	if uniq := slices.Compact(slices.Sorted(slices.Values(chain))); len(uniq) != len(chain) {
		fail(key, fmt.Sprintf("Cause %v に同じノードが 2 回出る", chain))
	}
}

// checkRows は order のノードを 1 回ずつ出し、ノードの行には `*` が 1 つだけ、つなぐ行には無いことを見る。
// order には辺の無いノードも混ぜ、辺のあるノードを落とすこともある。
func checkRows(fail func(key, msg string), r *rand.Rand, g *Graph, nodes []string) {
	order := slices.Clone(nodes)
	if r.IntN(3) == 0 {
		order = append(order, "z")
	}
	if len(order) > 1 && r.IntN(3) == 0 {
		order = order[1:]
	}
	r.Shuffle(len(order), func(i, j int) { order[i], order[j] = order[j], order[i] })
	dashed := func(string, string) bool { return r.IntN(2) == 0 }

	rows := g.Rows(order, dashed)

	emitted := []string{}
	for _, row := range rows {
		stars := strings.Count(row.Lanes, "*")
		switch {
		case row.Node == "" && stars != 0:
			fail("", fmt.Sprintf("Rows(%v): つなぐ行 %q に * がある", order, row.Lanes))
		case row.Node != "" && stars != 1:
			fail("", fmt.Sprintf("Rows(%v): ノードの行 %q の * が 1 つでない", order, row.Lanes))
		}
		if row.Node != "" {
			emitted = append(emitted, row.Node)
		}
	}
	if !reflect.DeepEqual(slices.Sorted(slices.Values(emitted)), slices.Sorted(slices.Values(order))) {
		fail("", fmt.Sprintf("Rows(%v) が出したノード %v が order と違う", order, emitted))
	}
}
