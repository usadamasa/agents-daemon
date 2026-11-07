// 依存図の右の列 (#420)。辺でつながるノードは依存図に描き、辺の無いノードは図の外の列に縦 1 列で並べる。
// 並び順はバイナリの column_order が持ち、ここは見えているノードを分けてその順に並べるだけにする。

// partition は nodes を辺の端にあるもの (connected、受け取った順) と辺の無いもの (column) に分ける。column は
// order (column_order) の順で、order に無いノード (column_order を出さない古いバイナリ) は受け取った順で後ろに足す。
export function partition(nodes, links, order) {
  const linked = new Set(links.flatMap((l) => [l.before, l.after]));
  const rank = new Map((order ?? []).map((key, i) => [key, i]));
  const connected = nodes.filter((n) => linked.has(n.key));
  const column = nodes
    .filter((n) => !linked.has(n.key))
    .map((n, i) => ({ n, at: rank.get(n.key) ?? rank.size + i }))
    .sort((a, b) => a.at - b.at)
    .map(({ n }) => n);
  return { connected, column };
}
