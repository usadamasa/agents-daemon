// 線のある図の配置。dagre で箱と枠と辺の経路を決める。段はバイナリが数えた depth をそのまま使う。
import * as dagre from "./vendor/dagre.esm.js";
import { fitEdges } from "./ports.js";

export const NODE_W = 240;
export const NODE_H = 72;
export const MARGIN_X = 64;
export const MARGIN_Y = 24;
const GAP_X = 48;
const GAP_Y = 44;

// rankByDepth はバイナリの段 (前提をたどった最長の深さ、根が 0。完了を隠す図では folded で数え直したもの) を
// そのまま段にする ranker。dagre の組み込みの
// ranker はどれも前提の無いノードを下流の直前へ寄せる (A→B→C と D→C で D が B の段に来る) ので使わない。
// layout は辺のラベルの場所を空けるために辺の minlen を 2 倍にし、枠の入れ子の分 (nodeRankFactor) を更に掛ける。
// depth にも同じ倍率を掛けて辺の minlen を満たす (@dagrejs/dagre 3.1.3 の内部の前提。満たさないと layout が落ちる)。
// 枠の上下の境界と入れ子の根 (dummy のノード) は depth を持たないので、nesting の辺の minlen を満たす段へ詰める。
function rankByDepth(depth) {
  return (g) => {
    const scale = 2 * (g.graph().nodeRankFactor ?? 1);
    const real = (v) => !g.node(v).dummy;
    for (const v of g.nodes()) g.node(v).rank = real(v) ? (depth.get(v) ?? 0) * scale : undefined;
    rankBorders(g, real);
  };
}

// rankBorders は dummy のノードの段を、辺 v→w の rank(w) >= rank(v) + minlen を満たす中で実ノードに最も近い段にする。
// 下の境界は実ノードから辺をたどって着くもので、入ってくる辺の端より下へ押す。それ以外 (上の境界と根) は
// 出ていく辺の端より上へ引く。境界どうしの鎖は入れ子の深さだけ続くので、ノードの数だけ回せば収まる。
function rankBorders(g, real) {
  const lower = new Set();
  const stack = g.nodes().filter(real);
  while (stack.length > 0) {
    for (const w of g.successors(stack.pop()) ?? []) {
      if (!real(w) && !lower.has(w)) {
        lower.add(w);
        stack.push(w);
      }
    }
  }
  const edges = g.edges().filter((e) => !real(e.v) || !real(e.w));
  for (let round = 0, changed = true; changed && round <= g.nodeCount(); round++) {
    changed = false;
    for (const e of edges) {
      const minlen = g.edge(e).minlen ?? 1;
      const v = g.node(e.v);
      const w = g.node(e.w);
      if (!real(e.v) && !lower.has(e.v) && w.rank !== undefined && (v.rank === undefined || v.rank > w.rank - minlen)) {
        v.rank = w.rank - minlen;
        changed = true;
      }
      if (lower.has(e.w) && v.rank !== undefined && (w.rank === undefined || w.rank < v.rank + minlen)) {
        w.rank = v.rank + minlen;
        changed = true;
      }
    }
  }
}

// transposeOrder は dagre の並べ替え (barycenter の sweep) の後に、同じ段で隣り合う 2 つを、入れ替えると上下の段との
// 辺の交差が減るあいだ入れ替える (Gansner らの transpose)。dagre は sweep の局所解に留まり、同じ枠の 2 つの箱へ
// 左右の別の枠から入る辺がたすき掛けのまま残ることがある。
// 入れ替えるのは親が同じで、どちらも枠の境界 (dummy が "border") でないものだけにする。枠の中身が左右の境界の間に
// 続いて並ぶ前提を崩さないため。交差が厳密に減るときだけ入れ替えるので、交差の無い図は動かない。
// dagre の customOrder の引数、order 後の node の rank / order / dummy、辺が隣の段どうしだけを結ぶことは
// @dagrejs/dagre 3.1.3 の内部の前提。
function transposeOrder(g, order) {
  order(g);
  const layers = [];
  for (const v of g.nodes()) {
    const n = g.node(v);
    if (typeof n.rank !== "number" || (g.children(v) ?? []).length > 0) continue;
    (layers[n.rank] ??= []).push(v);
  }
  for (const row of layers) row?.sort((a, b) => g.node(a).order - g.node(b).order);
  const movable = (v) => g.node(v).dummy !== "border";
  for (let changed = true; changed; ) {
    changed = false;
    for (const row of layers) {
      for (let i = 0; row && i + 1 < row.length; i++) {
        const [v, w] = [row[i], row[i + 1]];
        if (g.parent(v) !== g.parent(w) || !movable(v) || !movable(w)) continue;
        if (pairCrossings(g, w, v) >= pairCrossings(g, v, w)) continue;
        [row[i], row[i + 1]] = [w, v];
        [g.node(v).order, g.node(w).order] = [g.node(w).order, g.node(v).order];
        changed = true;
      }
    }
  }
  swapBlocks(g, layers);
}

// swapBlocks は同じ親の隣り合う 2 つの子 (枠ならその中身と境界ごと) を全部の段で入れ替え、辺の交差の数を増やさずに
// 辺の横の長さ (段どうしの order の差の和) が減るあいだ続ける。dagre の並べ替えは交差の数だけを見るので、交差の数が
// 同じ並びのうち、辺でつながる枠を互いに遠い端へ置いたものに留まることがある。
// 子の左右の順がどの段でも同じ親だけを扱う。その順で隣り合う 2 つは、両方が載る段で続いて並ぶので、段ごとに 2 つの
// 区間を入れ替えれば枠の中身が左右の境界の間に続いて並ぶ前提を崩さない。
function swapBlocks(g, layers) {
  const parents = [undefined, ...g.nodes().filter((v) => (g.children(v) ?? []).length > 0)];
  let score = scoreOrder(g, layers);
  for (let changed = true; changed; ) {
    changed = false;
    for (const p of parents) {
      const owner = ownerMap(g, layers, p);
      const seq = siblingOrder(layers, owner);
      for (let i = 0; seq && i + 1 < seq.length; i++) {
        const undo = swapPair(g, layers, owner, seq[i], seq[i + 1]);
        const next = scoreOrder(g, layers);
        if (next.crossings < score.crossings || (next.crossings === score.crossings && next.length < score.length)) {
          score = next;
          [seq[i], seq[i + 1]] = [seq[i + 1], seq[i]];
          changed = true;
        } else {
          undo();
        }
      }
    }
  }
}

// ownerMap は段のノードのうち p の子孫 (p の枠の境界を除く) を、それを含む p の子へ写す。
function ownerMap(g, layers, p) {
  const owner = new Map();
  for (const row of layers) {
    for (const v of row ?? []) {
      let c = v;
      while (c !== undefined && g.parent(c) !== p) c = g.parent(c);
      if (c !== undefined && g.node(c).dummy !== "border") owner.set(v, c);
    }
  }
  return owner;
}

// siblingOrder は子の左右の順を返す。段によって順が食い違うなら null。
function siblingOrder(layers, owner) {
  const left = new Set();
  const rest = [];
  for (const row of layers) {
    const runs = [];
    for (const v of row ?? []) {
      const c = owner.get(v);
      if (c !== undefined && runs.at(-1) !== c) runs.push(c);
    }
    for (let i = 0; i < runs.length; i++) {
      for (let j = i + 1; j < runs.length; j++) {
        if (runs[i] === runs[j] || left.has(`${runs[j]}\0${runs[i]}`)) return null;
        left.add(`${runs[i]}\0${runs[j]}`);
      }
    }
    for (const c of runs) if (!rest.includes(c)) rest.push(c);
  }
  // 段ごとの左右の関係を 1 本の順に並べる。どれも左に何かを要するなら関係が輪になっている。
  const seq = [];
  while (rest.length > 0) {
    const i = rest.findIndex((c) => !rest.some((d) => left.has(`${d}\0${c}`)));
    if (i < 0) return null;
    seq.push(...rest.splice(i, 1));
  }
  return seq;
}

// swapPair は段ごとに x の区間と、すぐ右に続く y の区間を入れ替え、元へ戻す関数を返す。
function swapPair(g, layers, owner, x, y) {
  const saved = [];
  for (const row of layers) {
    if (!row) continue;
    const xs = row.filter((v) => owner.get(v) === x);
    const ys = row.filter((v) => owner.get(v) === y);
    if (xs.length === 0 || ys.length === 0) continue;
    saved.push([row, row.slice()]);
    row.splice(row.indexOf(xs[0]), xs.length + ys.length, ...ys, ...xs);
    row.forEach((v, i) => (g.node(v).order = i));
  }
  return () => {
    for (const [row, old] of saved) {
      row.splice(0, row.length, ...old);
      row.forEach((v, i) => (g.node(v).order = i));
    }
  };
}

// scoreOrder は隣の段どうしの辺の交差の数 (辺の weight の積) と、辺の両端の order の差の和を数える。
function scoreOrder(g, layers) {
  let crossings = 0;
  let length = 0;
  for (const row of layers) {
    const edges = (row ?? []).flatMap((v) => g.outEdges(v) ?? []);
    for (let i = 0; i < edges.length; i++) {
      const a = edges[i];
      length += Math.abs(g.node(a.v).order - g.node(a.w).order);
      for (let j = i + 1; j < edges.length; j++) {
        const b = edges[j];
        const dv = g.node(a.v).order - g.node(b.v).order;
        const dw = g.node(a.w).order - g.node(b.w).order;
        if (dv * dw < 0) crossings += (g.edge(a).weight ?? 1) * (g.edge(b).weight ?? 1);
      }
    }
  }
  return { crossings, length };
}

// pairCrossings は left を right のすぐ左に置いたときの、left の辺と right の辺どうしの交差の数 (辺の weight の積)。
function pairCrossings(g, left, right) {
  const count = (ends, end) => {
    let n = 0;
    for (const a of ends(left) ?? []) {
      for (const b of ends(right) ?? []) {
        if (g.node(end(a)).order > g.node(end(b)).order) n += (g.edge(a).weight ?? 1) * (g.edge(b).weight ?? 1);
      }
    }
    return n;
  };
  return count((v) => g.inEdges(v), (e) => e.v) + count((v) => g.outEdges(v), (e) => e.w);
}

// layout は線のある図のノード (tree.nodes。辺でつながるノードと、同じ枠に引き入れた辺の無いノード) を dagre で配置し、
// 枠 (tree.frames) を compound の親にする。辺の無いほかのノードは図の下のセッションの枠 (renderGroups) か
// 右の列 (renderColumn) に並べる。depth は key → 段 (Graph.view の depth)。返すのは key → { x, y } (中心) と、
// 枠の key → { x, y, width, height } (中心と大きさ)、全体の width、height、辺の経路を持つ dagre の graph。
// 待ちの輪の辺は段が下へ進まない (輪の両端をバイナリが同じ段か逆の段に数える)。rankByDepth の段で dagre に渡すと
// layout が落ちるので、段が下へ進む辺だけを dagre に渡し、残り (back) は箱の中心どうしを結ぶ点列で返す。
export function layout(tree, links, depth) {
  const connected = tree.nodes;
  const g = new dagre.graphlib.Graph({ compound: true });
  g.setGraph({ rankdir: "TB", ranker: rankByDepth(depth), nodesep: GAP_X, ranksep: GAP_Y, marginx: MARGIN_X, marginy: MARGIN_Y });
  g.setDefaultEdgeLabel(() => ({}));
  for (const n of connected) g.setNode(n.key, { width: NODE_W, height: NODE_H });
  for (const f of tree.frames) g.setNode(f.key, {});
  for (const f of tree.frames) if (f.parent) g.setParent(f.key, f.parent);
  for (const [key, parent] of tree.parentOf) g.setParent(key, parent);
  const rest = [];
  for (const l of links) {
    if (!g.hasNode(l.before) || !g.hasNode(l.after)) continue;
    if ((depth.get(l.after) ?? 0) > (depth.get(l.before) ?? 0)) g.setEdge(l.before, l.after);
    else rest.push(l);
  }
  dagre.layout(g, { customOrder: transposeOrder });
  // renderGraph は connected が空なら layout を呼ばないので、ここでは空を見ない。
  fitEdges(g, MARGIN_X, MARGIN_Y);
  const pos = new Map(connected.map((n) => [n.key, g.node(n.key)]));
  const boxes = new Map(tree.frames.map((f) => [f.key, g.node(f.key)]));
  const back = rest.map((l) => {
    const a = pos.get(l.before);
    const b = pos.get(l.after);
    return { ...l, points: [{ x: a.x, y: a.y }, { x: (a.x + b.x) / 2, y: (a.y + b.y) / 2 }, { x: b.x, y: b.y }] };
  });
  return { g, pos, boxes, back, width: g.graph().width, height: g.graph().height };
}
