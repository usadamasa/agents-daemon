// 図の領域と右の列へのカードの振り分け (#420、#467)。辺でつながるノードは依存図に、辺の無い動いているノードは図の
// 領域のセッションの枠に、辺の無い終わったノードは右の列に置く。
// 並び順はバイナリの column_order が持ち、ここは見えているノードを分けてその順に並べるだけにする。

const ENDED =new Set(["exited", "completed"]);

// partition は nodes を辺の端にあるもの (connected、受け取った順) と、辺の無い動いているもの (free)、辺の無い
// 終わったもの (ended) に分ける。辺のある終わったノードは線の端を残すため connected に入る。free と ended は
// order (column_order) の順で、order に無いノード (column_order を出さない古いバイナリ) は受け取った順で後ろに足す。
export function partition(nodes, links, order) {
  const linked = new Set(links.flatMap((l) => [l.before, l.after]));
  const rank = new Map((order ?? []).map((key, i) => [key, i]));
  const connected = nodes.filter((n) => linked.has(n.key));
  const lineless = nodes
    .filter((n) => !linked.has(n.key))
    .map((n, i) => ({ n, at: rank.get(n.key) ?? rank.size + i }))
    .sort((a, b) => a.at - b.at)
    .map(({ n }) => n);
  return {
    connected,
    free: lineless.filter((n) => !ENDED.has(n.state)),
    ended: lineless.filter((n) => ENDED.has(n.state)),
  };
}

// groupBySession は辺の無い動いているノードをセッションごとの枠にまとめる。枠の順は最初のカードの順 (column_order) で、
// 名前は重なりうるので session_id で分ける。参照はセッションを持たないので「参照」の枠に集める。
export function groupBySession(nodes) {
  const groups = new Map();
  for (const n of nodes) {
    const key = n.kind === "ref" ? "ref" : n.session_id;
    if (!groups.has(key)) groups.set(key, { key, label: n.kind === "ref" ? "参照" : n.session, nodes: [] });
    groups.get(key).nodes.push(n);
  }
  return [...groups.values()];
}

// 枠の寸法。style.css の .group と揃える (カードの幅 240 と高さ 72、余白 12、枠線 1、見出し 16、間 10)。
export const GROUP_W = 266;
const GROUP_CHROME = 42;
const GROUP_STEP = 82;
const GROUP_GAP = 16;

export function groupHeight(cards) {
  return GROUP_CHROME + GROUP_STEP * cards;
}

// placeGroups は枠を left から右へ並べ、右端と左の余白を足して limit を超える枠を次の行へ送る。行の先頭の枠は
// 超えても置く。places は groups と同じ順の左上で、right と bottom は置いた枠の右端と下端。
export function placeGroups(groups, left, top, limit) {
  const places = [];
  let x = left;
  let y = top;
  let rowH = 0;
  let right = left;
  for (const g of groups) {
    if (x > left && x + GROUP_W + left > limit) {
      x = left;
      y += rowH + GROUP_GAP;
      rowH = 0;
    }
    places.push({ x, y });
    right = Math.max(right, x + GROUP_W);
    rowH = Math.max(rowH, groupHeight(g.nodes.length));
    x += GROUP_W + GROUP_GAP;
  }
  return { places, right, bottom: y + rowH };
}

// fitLimit は placeGroups に渡す limit を、図全体を view に収める拡大率がいちばん大きくなる列の数から決める。
// left と bottom は枠の外の余白、top は枠を置き始める高さ、graph は枠の上にある線のある図の大きさ。
// 同じ拡大率なら列を多くして行を減らす (100% で収まる図は view の幅いっぱいまで並べる)。拡大率は下限 (MIN_ZOOM) で
// 切らずに比べ、下限でも収まらない図でも縦横の釣り合う並べ方を選ぶ。
export function fitLimit(groups, { left, top, bottom, graph, view }) {
  let best = { zoom: -1, limit: 0 };
  for (let k = 1; k <= groups.length; k++) {
    const limit = 2 * left + k * GROUP_W + (k - 1) * GROUP_GAP;
    const placed = placeGroups(groups, left, top, limit);
    const width = Math.max(graph.width, placed.right + left);
    const height = Math.max(graph.height, placed.bottom + bottom);
    const zoom = Math.min(1, view.width / width, view.height / height);
    if (zoom >= best.zoom) best = { zoom, limit };
  }
  return best.limit;
}
