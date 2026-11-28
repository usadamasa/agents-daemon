// 図の領域と右の列へのカードの振り分け (#420、#467)。辺でつながるノードは依存図に、辺の無い動いているノードは図の
// 領域のセッションの枠に、辺の無い終わったノードは右の列に置く。依存図の中も同じ枠で囲み、辺のあるノードを持つ枠は
// 辺の無いノードも依存図の側へ引き入れる (frameTree)。
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

// frameOf はノードの枠と区切り (#633)。枠は rex の session (見出しは session の label、区切りは window)、置き場の
// 分からない生きているノード (区切りは Claude のセッション。名前は重なりうるので session_id で分ける)、参照の 3 種類。
// window に置かれていない block と参照の区切りは小見出しを持たない。生きているノードは rex の block からしか立たない
// ので、置き場が無いのは daemon が置き場を書けていないとき (#662)。placement (deps の rex_placement) が無ければ
// daemon が置き場を書く前のものなので、見出しで更新を促す。
// refs (placeRefs) を渡すと、issue / PR の参照は結びつけたノードの枠と区切りに入り、どの辺にもつながらない参照は
// 孤立を示す枠に入る (#727)。rex の置き場を持つ置き場のノード (末端を集めた生きているセッションの sid:) は、その rex の枠に入る。
function frameOf(n, placement, refs) {
  const anchor = refs?.anchor.get(n.key);
  if (anchor) return frameOf(anchor, placement);
  if (refs?.unlinked.has(n.key)) return { key: "unlinked", label: UNLINKED_LABEL, part: "", partLabel: "" };
  if (n.rex) return { key: `rex:${n.rex.session_id}`, label: n.rex.session_label, part: n.rex.window_id ?? "", partLabel: n.rex.window_label ?? "", rex: n.rex.session_id };
  if (n.kind === "ref") return { key: "ref", label: "Refs", part: "", partLabel: "" };
  const label = placement ? "Rex placement unknown" : "Rex placement unknown: update the daemon";
  return { key: "unplaced", label, part: n.session_id, partLabel: n.session };
}

export const UNLINKED_LABEL = "Unlinked refs";

// placeRefs は issue / PR の参照 (ref_kind を持つノード) の置き場を、完了を隠すかに関わらず全部のノードと辺から決める。
// anchor は参照の key → 枠を借りるノードで、次の順に最初に当たるものにする (同じ順位なら links の順)。
//   1. 上流 (辺の before。daemon が導く辺なら PR を作ったセッションのタスク) のうち、セッションが生きていて rex の置き場のあるもの
//   2. 下流 (参照を待っている側) のうち、rex の置き場のあるもの
//   3. 上流のうち、rex の置き場のあるもの (作ったセッションが終わっていて、待っている側が無いとき)
// どれにも当たらない参照は、辺でつながる参照 (上流、下流の順) が結びついたノードを借りる。
// unlinked はどの辺にもつながらない参照の key。
export function placeRefs(nodes, links) {
  const byKey = new Map(nodes.map((n) => [n.key, n]));
  const anchor = new Map();
  const unlinked = new Set();
  const placed = (n) => Boolean(n?.rex);
  const rest = [];
  for (const n of nodes) {
    if (!n.ref_kind) continue;
    const ups = links.filter((l) => l.after === n.key).map((l) => byKey.get(l.before)).filter(Boolean);
    const downs = links.filter((l) => l.before === n.key).map((l) => byKey.get(l.after)).filter(Boolean);
    if (ups.length === 0 && downs.length === 0) {
      unlinked.add(n.key);
      continue;
    }
    const to = ups.find((u) => placed(u) && u.live) ?? downs.find(placed) ?? ups.find(placed);
    if (to) anchor.set(n.key, to);
    else rest.push({ key: n.key, near: [...ups, ...downs].filter((m) => m.ref_kind) });
  }
  // 参照どうしの鎖は、結びついた参照から 1 段ずつ広げる。
  for (let changed = true; changed; ) {
    changed = false;
    for (const r of rest) {
      if (anchor.has(r.key)) continue;
      const via = r.near.find((m) => anchor.has(m.key));
      if (via) {
        anchor.set(r.key, anchor.get(via.key));
        changed = true;
      }
    }
  }
  return { anchor, unlinked };
}

// groupByRex は辺の無い動いているノードを枠にまとめ、枠の中を区切りに分ける。枠と区切りの順は最初のカードの順
// (column_order)。nodes は枠の中のカードを区切りの順に並べたもの。placement は deps の rex_placement、refs は placeRefs。
export function groupByRex(nodes, placement, refs) {
  const groups = new Map();
  for (const n of nodes) {
    const f = frameOf(n, placement, refs);
    if (!groups.has(f.key)) groups.set(f.key, { key: f.key, label: f.label, rex: f.rex, parts: new Map() });
    const parts = groups.get(f.key).parts;
    if (!parts.has(f.part)) parts.set(f.part, { label: f.partLabel, nodes: [] });
    parts.get(f.part).nodes.push(n);
  }
  return [...groups.values()].map((g) => {
    const parts = [...g.parts.values()];
    return { key: g.key, label: g.label, rex: g.rex, parts, nodes: parts.flatMap((p) => p.nodes) };
  });
}

// frameTree は線のある図 (dagre) で描く枠を決める。辺のあるノードを frameOf の枠 (session) と区切り (window) に入れ、
// その枠に属する辺の無いノード (free) も同じ枠へ引き入れる。終わったノードは rex の置き場があるときだけ枠に入れ、
// 同じ window の線が枠の外から長く横切らないようにする。参照は placeRefs が結びつけたノードの枠に入れる。結びつけた
// ノードが完了で隠れていても、その枠を作って参照を入れる。rex の置き場を持つ置き場のノード (sid:) も枠に入れる。
// 結びつけていない参照と、置き場の無い終わったノードは枠に入れない。
// frames は枠の { key, label, level, parent, rex } (level は h2 / h3、rex は rex の session の id)、parentOf はノードの
// key → 直の親の枠の key。
// 小見出しの無い区切りは枠を作らず、ノードを session の枠へ直に入れる。nodes は図に入るノード (linked の後ろに
// 引き入れた free を順に足す)、free は下の枠に残るもの。枠の key は "\0" で始め、ノードの key と重ならない。
// placement は deps の rex_placement、refs は placeRefs。
export function frameTree(linked, free, placement, refs) {
  const framable = (n) => (n.kind === "ref" ? Boolean(refs?.anchor.has(n.key) || n.rex) : !ENDED.has(n.state) || Boolean(n.rex));
  const frameIn = (n) => frameOf(n, placement, refs);
  const used = new Set(linked.filter(framable).map((n) => frameIn(n).key));
  const pulled = free.filter((n) => used.has(frameIn(n).key));
  const nodes = [...linked, ...pulled];
  const frames = new Map();
  const parentOf = new Map();
  for (const n of nodes) {
    if (!framable(n)) continue;
    const f = frameIn(n);
    const top = `\0${f.key}`;
    if (!frames.has(top)) frames.set(top, { key: top, label: f.label, level: "h2", rex: f.rex });
    let parent = top;
    if (f.partLabel !== "") {
      parent = `\0${f.key}\0${f.part}`;
      if (!frames.has(parent)) frames.set(parent, { key: parent, label: f.partLabel, level: "h3", parent: top, rex: f.rex });
    }
    parentOf.set(n.key, parent);
  }
  return { nodes, free: free.filter((n) => !used.has(frameIn(n).key)), frames: [...frames.values()], parentOf };
}

// TONES は枠の色の数。style.css の :is(.frame, .group).tone-N と揃える。
export const TONES = 4;

// rexTones は rex の session の id → 枠の色の番号。色は id の hash だけで決め、session が閉じたり図に出る session が
// 変わったりしても、残りの session の色を動かさない。別の session が同じ色になることはある。
export function rexTones(nodes) {
  const ids = new Set(nodes.filter((n) => n.rex).map((n) => n.rex.session_id));
  return new Map([...ids].map((id) => [id, toneOf(id)]));
}

// toneOf は id の FNV-1a (32 bit) を色の数で割った余り。
function toneOf(id) {
  let h = 0x811c9dc5;
  for (let i = 0; i < id.length; i++) {
    h ^= id.charCodeAt(i);
    h = Math.imul(h, 0x01000193) >>> 0;
  }
  return h % TONES;
}

// heads は枠の小見出しの数。小見出しの無い区切りは数えない。
export function heads(group) {
  return (group.parts ?? []).filter((p) => p.label !== "").length;
}

// 枠の寸法。style.css の .group と揃える (カードの幅 240 と高さ 72、余白 12、枠線 1、見出し 16、間 10、
// 小見出し 16 と下の間 4)。
export const GROUP_W = 266;
const GROUP_CHROME = 42;
const GROUP_STEP = 82;
const GROUP_GAP = 16;
const HEAD_STEP = 20;

export function groupHeight(cards, subheads = 0) {
  return GROUP_CHROME + GROUP_STEP * cards + HEAD_STEP * subheads;
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
    rowH = Math.max(rowH, groupHeight(g.nodes.length, heads(g)));
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
