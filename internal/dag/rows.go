package dag

import (
	"sort"
	"strings"
)

// Row は図の 1 行。Lanes は行頭の罫線で、ノードの行は `*` がそのノードのレーンの位置。Node が空なら辺をつなぐだけの行。
// レーン 1 本は 2 桁 (記号と詰め) で、右端の空きは詰める。
type Row struct {
	Lanes string `json:"lanes"`
	Node  string `json:"node,omitempty"`
}

// Rows は order のノードを位相順に 1 行ずつ並べ、辺をレーンで描く (git log --graph の形、上流が上)。
// dashed が true の辺は破線にする。
//
// 次に出すノードは、直前に出したノードの下流で上流が出揃ったもの (鎖を続けてレーンを短くする)、無ければ
// 上流が出揃ったもののうち order で先のもの。出せるものが無ければ (待ちの輪) 残りのうち Depth が最も小さく
// order で先のものを出し、そのとき出ていない上流からの辺は描かない。
func (g *Graph) Rows(order []string, dashed func(before, after string) bool) []Row {
	l := &layout{g: g, dashed: dashed, index: map[string]int{}, emitted: map[string]bool{}, indeg: map[string]int{}, rows: []Row{}}
	for i, k := range order {
		l.index[k] = i
	}
	for _, k := range order {
		for _, p := range g.preds[k] {
			if _, ok := l.index[p]; ok {
				l.indeg[k]++
			}
		}
	}
	last := ""
	for range order {
		k := l.next(last, order)
		l.emit(k)
		last = k
	}
	return l.rows
}

// lane は描いている途中の辺 1 本。to は向かう先のノードで、空なら閉じた後の空き。
type lane struct {
	to     string
	dashed bool
}

// layout はレーンの割り当ての途中の状態。
type layout struct {
	g       *Graph
	dashed  func(before, after string) bool
	index   map[string]int
	emitted map[string]bool
	indeg   map[string]int
	lanes   []lane
	rows    []Row
}

// next は次に出すノード。
func (l *layout) next(last string, order []string) string {
	if last != "" {
		for _, s := range l.succsByOrder(last) {
			if !l.emitted[s] && l.indeg[s] == 0 {
				return s
			}
		}
	}
	for _, k := range order {
		if !l.emitted[k] && l.indeg[k] == 0 {
			return k
		}
	}
	best := ""
	for _, k := range order {
		if !l.emitted[k] && (best == "" || l.g.Depth(k) < l.g.Depth(best)) {
			best = k
		}
	}
	return best
}

// succsByOrder は key の下流のうち order にあるものを order の順で返す。
func (l *layout) succsByOrder(key string) []string {
	out := []string{}
	for _, s := range l.g.succs[key] {
		if _, ok := l.index[s]; ok {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return l.index[out[i]] < l.index[out[j]] })
	return out
}

// emit はノードの行を足し、上流のレーンを寄せ、下流のレーンを開く。
func (l *layout) emit(key string) {
	l.emitted[key] = true
	incoming := l.incoming(key)
	idx := 0
	if len(incoming) == 0 {
		idx = l.open(0, key, false)
	} else {
		idx = incoming[0]
		if len(incoming) > 1 {
			l.rows = append(l.rows, Row{Lanes: l.joinRow(idx, incoming[1:], "┘", "┴")})
			for _, i := range incoming[1:] {
				l.lanes[i] = lane{}
			}
		}
	}
	l.rows = append(l.rows, Row{Lanes: l.nodeRow(idx), Node: key})

	outs := []string{}
	for _, s := range l.succsByOrder(key) {
		l.indeg[s]--
		if !l.emitted[s] {
			outs = append(outs, s)
		}
	}
	if len(outs) == 0 {
		l.lanes[idx] = lane{}
		l.trim()
		return
	}
	l.lanes[idx] = lane{to: outs[0], dashed: l.dashed(key, outs[0])}
	opened := []int{}
	for _, s := range outs[1:] {
		opened = append(opened, l.open(idx+1, s, l.dashed(key, s)))
	}
	if len(opened) > 0 {
		l.rows = append(l.rows, Row{Lanes: l.joinRow(idx, opened, "┐", "┬")})
	}
}

// incoming は key へ向かうレーンの位置 (左から)。
func (l *layout) incoming(key string) []int {
	out := []int{}
	for i, ln := range l.lanes {
		if ln.to == key {
			out = append(out, i)
		}
	}
	return out
}

// open は from から右で最初の空きに to へ向かうレーンを置き (無ければ右端に足し)、その位置を返す。
func (l *layout) open(from int, to string, dashed bool) int {
	for i := from; i < len(l.lanes); i++ {
		if l.lanes[i].to == "" {
			l.lanes[i] = lane{to: to, dashed: dashed}
			return i
		}
	}
	l.lanes = append(l.lanes, lane{to: to, dashed: dashed})
	return len(l.lanes) - 1
}

// trim は右端の空きを詰める。
func (l *layout) trim() {
	for len(l.lanes) > 0 && l.lanes[len(l.lanes)-1].to == "" {
		l.lanes = l.lanes[:len(l.lanes)-1]
	}
}

// nodeRow はノードの行: idx に `*`、ほかの通り抜けるレーンは縦線。
func (l *layout) nodeRow(idx int) string {
	var b strings.Builder
	for i, ln := range l.lanes {
		if i == idx {
			b.WriteString("* ")
			continue
		}
		b.WriteString(vertical(ln))
		b.WriteString(" ")
	}
	return strings.TrimRight(b.String(), " ")
}

// joinRow は idx のレーンと others (右にある、昇順) を横線でつなぐ行。合流なら others は寄せる上流のレーンで
// 端が `┘`、分岐なら開いた下流のレーンで端が `┐`。間を通り抜けるレーンとは `┼` で交差する。
// 横線は、その右で最初に当たる others のレーンが破線なら `┄`。
func (l *layout) joinRow(idx int, others []int, end, mid string) string {
	last := others[len(others)-1]
	isOther := map[int]bool{}
	for _, i := range others {
		isOther[i] = true
	}
	horizontal := func(i int) string {
		for _, o := range others {
			if o > i {
				if l.lanes[o].dashed {
					return "┄"
				}
				return "─"
			}
		}
		return "─"
	}
	var b strings.Builder
	for i, ln := range l.lanes {
		switch {
		case i == idx:
			b.WriteString("├")
		case i == last:
			b.WriteString(end)
		case isOther[i]:
			b.WriteString(mid)
		case i > idx && i < last && ln.to != "":
			b.WriteString("┼")
		case i > idx && i < last:
			b.WriteString(horizontal(i))
		default:
			b.WriteString(vertical(ln))
		}
		if i >= idx && i < last {
			b.WriteString(horizontal(i))
		} else {
			b.WriteString(" ")
		}
	}
	return strings.TrimRight(b.String(), " ")
}

// vertical は通り抜けるレーンの縦線。空きは空白。
func vertical(ln lane) string {
	switch {
	case ln.to == "":
		return " "
	case ln.dashed:
		return "┆"
	default:
		return "│"
	}
}
