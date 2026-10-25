// Package dag は依存の有向グラフ (before が済まないと after を始められない) の段・下流・輪・鎖を数える。
// ノードは文字列の Key で、何のノードかは知らない。依存図 (internal/deps) がタスクの依存を組んで使う (#134)。
//
// 辺は渡す側が自由に書くので循環しうる。どの数え方も、たどっている途中のノードへ戻る辺を無視して止まる。
package dag

import "sort"

// Graph は辺から組んだ前後の表。
type Graph struct {
	preds  map[string][]string
	succs  map[string][]string
	depths map[string]int
	chains map[string][]string
}

// New は辺 (before, after) から表を組む。前後のノードは less の順に並べ、鎖を選ぶときの順にする。
func New(links [][2]string, less func(a, b string) bool) *Graph {
	g := &Graph{preds: map[string][]string{}, succs: map[string][]string{}, depths: map[string]int{}, chains: map[string][]string{}}
	for _, l := range links {
		g.succs[l[0]] = append(g.succs[l[0]], l[1])
		g.preds[l[1]] = append(g.preds[l[1]], l[0])
	}
	for _, m := range []map[string][]string{g.preds, g.succs} {
		for _, keys := range m {
			sort.Slice(keys, func(i, j int) bool { return less(keys[i], keys[j]) })
		}
	}
	return g
}

// Depth は前提をたどった最長の深さ (段)。前提の無いノードは 0。
func (g *Graph) Depth(key string) int {
	d, _ := g.depthFrom(key, map[string]bool{})
	return d
}

// depthFrom は深さと、輪で辺を切らずに数えられたか (clean) を返す。切った値はたどり始めの位置で変わるので
// 覚えない (どのノードから数えても同じ値にする)。
func (g *Graph) depthFrom(key string, onPath map[string]bool) (int, bool) {
	if d, ok := g.depths[key]; ok {
		return d, true
	}
	onPath[key] = true
	d, clean := 0, true
	for _, p := range g.preds[key] {
		if onPath[p] {
			clean = false
			continue
		}
		pd, pc := g.depthFrom(p, onPath)
		d, clean = max(d, pd+1), clean && pc
	}
	delete(onPath, key)
	if clean {
		g.depths[key] = d
	}
	return d, clean
}

// Reach は key から下流へたどれるノード (key 自身は含めない) と、key へ戻ってくるか (輪の中にいるか)。
func (g *Graph) Reach(key string) (map[string]bool, bool) {
	seen := map[string]bool{}
	cyclic := false
	stack := append([]string{}, g.succs[key]...)
	for len(stack) > 0 {
		k := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if k == key {
			cyclic = true
			continue
		}
		if seen[k] {
			continue
		}
		seen[k] = true
		stack = append(stack, g.succs[k]...)
	}
	return seen, cyclic
}

// Cause は上流をたどった原因の鎖 (近い順)。open な上流のうち rank の小さいもの (同じなら less の順で先) を
// 1 つ選んでたどり、open な上流が無くなったら止まる。
func (g *Graph) Cause(key string, open func(string) bool, rank func(string) int) []string {
	chain := []string{}
	visited := map[string]bool{key: true}
	for cur := key; ; {
		next := ""
		for _, p := range g.preds[cur] {
			if !visited[p] && open(p) && (next == "" || rank(p) < rank(next)) {
				next = p
			}
		}
		if next == "" {
			return chain
		}
		chain = append(chain, next)
		visited[next] = true
		cur = next
	}
}

// Longest は key から下流をたどった最長の鎖 (key を含む)。長さが同じなら less の順で先のものを選ぶ。
func (g *Graph) Longest(key string) []string {
	chain, _ := g.longestFrom(key, map[string]bool{})
	return chain
}

// longestFrom は depthFrom と同じく、輪で辺を切らずに数えられた鎖だけを覚える。覚えないと菱形が重なった DAG で
// 経路の数だけたどり直す。
func (g *Graph) longestFrom(key string, onPath map[string]bool) ([]string, bool) {
	if chain, ok := g.chains[key]; ok {
		return chain, true
	}
	onPath[key] = true
	var best []string
	clean := true
	for _, s := range g.succs[key] {
		if onPath[s] {
			clean = false
			continue
		}
		tail, tc := g.longestFrom(s, onPath)
		clean = clean && tc
		if len(tail) > len(best) {
			best = tail
		}
	}
	delete(onPath, key)
	chain := append([]string{key}, best...)
	if clean {
		g.chains[key] = chain
	}
	return chain, clean
}
