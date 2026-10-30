package dag

import (
	"slices"
	"sort"
	"strings"

	"github.com/usadamasa/agents-daemon/internal/protocol"
)

// Row は図の 1 行 (形は protocol の schema が持つ)。レーン 1 本は 2 桁 (記号と詰め) で、閉じたレーンには右のレーンを
// 詰める。Marks の桁は rune の位置。
type Row = protocol.GraphRow

// Rows は order のノードを位相順に 1 行ずつ並べ、辺をレーンで描く (git log --graph の形、上流が上)。
// dashed が true の辺は破線にし、highlight が true の辺は罫線の桁を Marks に載せる。
//
// 次に出すノードは、直前に出したノードの下流で上流が出揃ったもの (鎖を続けてレーンを短くする)、無ければ
// 上流が出揃ったもののうち order で先のもの。出せるものが無ければ (待ちの輪) 残りのうち Depth が最も小さく
// order で先のものを出し、そのとき出ていない上流からの辺は描かない。
//
// レーンが閉じて空いたら、右のレーンを 1 本ずつ `┌─┘` の行で左へ詰める。空きを残すと、後から来た無関係の
// ノードが空きに入り、右を通る鎖に属しているように見えるため (#193)。
func (g *Graph) Rows(order []string, dashed, highlight func(before, after string) bool) []Row {
	l := &layout{g: g, dashed: dashed, highlight: highlight, index: map[string]int{}, emitted: map[string]bool{}, indeg: map[string]int{}, rows: []Row{}}
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
	marked bool
}

// layout はレーンの割り当ての途中の状態。lanes は emit の終わりには空きを持たない。
type layout struct {
	g         *Graph
	dashed    func(before, after string) bool
	highlight func(before, after string) bool
	index     map[string]int
	emitted   map[string]bool
	indeg     map[string]int
	lanes     []lane
	rows      []Row
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
		idx = l.open(lane{to: key})
	} else {
		idx = incoming[0]
		if len(incoming) > 1 {
			l.rows = append(l.rows, l.joinRow(idx, incoming[1:], "┘", "┴").row(""))
			for _, i := range incoming[1:] {
				l.lanes[i] = lane{}
			}
			// 外れたレーンは idx より右にあるので、詰めても idx は動かない。
			l.compact()
		}
	}
	l.rows = append(l.rows, l.nodeRow(idx).row(key))

	outs := []string{}
	for _, s := range l.succsByOrder(key) {
		l.indeg[s]--
		if !l.emitted[s] {
			outs = append(outs, s)
		}
	}
	if len(outs) == 0 {
		l.lanes[idx] = lane{}
		l.compact()
		return
	}
	l.lanes[idx] = l.edge(key, outs[0])
	opened := []int{}
	for _, s := range outs[1:] {
		opened = append(opened, l.open(l.edge(key, s)))
	}
	if len(opened) > 0 {
		l.rows = append(l.rows, l.joinRow(idx, opened, "┐", "┬").row(""))
	}
}

// edge は before から after へ向かうレーン。
func (l *layout) edge(before, after string) lane {
	return lane{to: after, dashed: l.dashed(before, after), marked: l.highlight(before, after)}
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

// open は右端にレーンを足し、その位置を返す (lanes は空きを持たない)。
func (l *layout) open(ln lane) int {
	l.lanes = append(l.lanes, ln)
	return len(l.lanes) - 1
}

// trim は右端の空きを詰める。
func (l *layout) trim() {
	for len(l.lanes) > 0 && l.lanes[len(l.lanes)-1].to == "" {
		l.lanes = l.lanes[:len(l.lanes)-1]
	}
}

// compact は空きの右にあるレーンを 1 本ずつ空きへ寄せ、寄せるたびにその行を足す。右に 2 本以上あっても
// 1 行で寄せない (`┌─┘` の端が重なって読めなくなる)。
func (l *layout) compact() {
	l.trim()
	for {
		gap := slices.IndexFunc(l.lanes, func(ln lane) bool { return ln.to == "" })
		if gap < 0 {
			return
		}
		from := gap + 1 + slices.IndexFunc(l.lanes[gap+1:], func(ln lane) bool { return ln.to != "" })
		l.rows = append(l.rows, l.shiftRow(gap, from).row(""))
		l.lanes[gap], l.lanes[from] = l.lanes[from], lane{}
		l.trim()
	}
}

// line は 1 行の罫線を桁ごとに組む。marks は桁ごとの印。
type line struct {
	cells []string
	marks []bool
}

func (b *line) put(cell string, marked bool) {
	b.cells = append(b.cells, cell)
	b.marks = append(b.marks, marked)
}

// row は右端の空白を落とした行。
func (b *line) row(node string) Row {
	n := len(b.cells)
	for n > 0 && b.cells[n-1] == " " {
		n--
	}
	var marks []int
	for i := range n {
		if b.marks[i] {
			marks = append(marks, i)
		}
	}
	return Row{Lanes: strings.Join(b.cells[:n], ""), Node: node, Marks: marks}
}

// pass は通り抜けるレーンの縦線と詰めの 2 桁を置く。
func (b *line) pass(ln lane) {
	b.put(vertical(ln), ln.to != "" && ln.marked)
	b.put(" ", false)
}

// nodeRow はノードの行: idx に `*`、ほかの通り抜けるレーンは縦線。`*` には印を付けない (mod が記号に置き換える)。
func (l *layout) nodeRow(idx int) *line {
	b := &line{}
	for i, ln := range l.lanes {
		if i == idx {
			b.put("*", false)
			b.put(" ", false)
			continue
		}
		b.pass(ln)
	}
	return b
}

// shiftRow は from のレーンを gap へ寄せる行: gap に `┌`、from に `┘`、間は横線。横線は寄せるレーンが破線なら `┄`。
func (l *layout) shiftRow(gap, from int) *line {
	moving := l.lanes[from]
	horizontal := "─"
	if moving.dashed {
		horizontal = "┄"
	}
	b := &line{}
	for i, ln := range l.lanes {
		switch {
		case i == gap:
			b.put("┌", moving.marked)
		case i == from:
			b.put("┘", moving.marked)
		case i > gap && i < from:
			b.put(horizontal, moving.marked)
		default:
			b.pass(ln)
			continue
		}
		if i < from {
			b.put(horizontal, moving.marked)
		} else {
			b.put(" ", false)
		}
	}
	return b
}

// joinRow は idx のレーンと others (右にある、昇順) を横線でつなぐ行。合流なら others は寄せる上流のレーンで
// 端が `┘`、分岐なら開いた下流のレーンで端が `┐`。間を通り抜けるレーンとは `┼` で交差する。
// 横線は、その右で最初に当たる others のレーンが破線なら `┄`。印は、印の付いた others のレーンとそこまでの横線、
// idx の `├` に付く。
func (l *layout) joinRow(idx int, others []int, end, mid string) *line {
	last := others[len(others)-1]
	isOther := map[int]bool{}
	anyMarked := false
	for _, i := range others {
		isOther[i] = true
		anyMarked = anyMarked || l.lanes[i].marked
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
	// toMarked は i より右に印の付いた others があるか (i の右の横線がその辺の一部か)。
	toMarked := func(i int) bool {
		for _, o := range others {
			if o > i && l.lanes[o].marked {
				return true
			}
		}
		return false
	}
	b := &line{}
	for i, ln := range l.lanes {
		switch {
		case i == idx:
			b.put("├", anyMarked || ln.marked)
		case i == last:
			b.put(end, ln.marked)
		case isOther[i]:
			b.put(mid, ln.marked)
		case i > idx && i < last && ln.to != "":
			b.put("┼", ln.marked || toMarked(i))
		case i > idx && i < last:
			b.put(horizontal(i), toMarked(i))
		default:
			b.pass(ln)
			continue
		}
		if i >= idx && i < last {
			b.put(horizontal(i), toMarked(i))
		} else {
			b.put(" ", false)
		}
	}
	return b
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
