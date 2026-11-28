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
  dagre.layout(g);
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
