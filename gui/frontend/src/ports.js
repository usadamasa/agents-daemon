// 辺の端をノードの辺に散らす。dagre は辺をノードの中心へ向けるので、同じノードに入る (出る) 辺の端が境界の 1 点に
// 集まり、矢じりが重なって本数が読めない。入ってくる辺の終点は上辺へ、出ていく辺の始点は下辺へ等間隔で置く
// (rankdir TB の前提)。
//
// 端の隣の点 (端に最も近い中の点) は port の真上・真下へ寄せ、端の区間を真っすぐにする。別の枠から入る辺の中の点は、
// dagre が行き先の近くに edgesep の間隔で詰めて置くので、そのままでは最後の区間が行き先を越えて折り返す (#707)。
//
// port は、各辺がノードへ近づいてくる線の並びで割り当てる。線は寄せる点の 1 つ手前の点 (anchor) からノードの中心の
// x へ向かう線分で、群れの中でノードに最も近い anchor の高さでの x の順に並べる。相手のノードの x の順や端の隣の点の
// x の順では、遠くから浅く入る辺と、すぐ上から入る辺がノードの手前で交わる。

// spreadPorts は edges ({ before, after, points }) の points の端を置き直した写しを返す。pos は key → ノードの中心。
// 端の隣の点は、両端の間に中の点が 2 つ以上あるときだけ動かす。中の点が 1 つの辺 (隣りの段どうし、待ちの輪の辺) は
// その点を両端が共有するので動かさない。
export function spreadPorts(edges, pos, width, height) {
  const out = edges.map((e) => ({ ...e, points: e.points.map((p) => ({ ...p })) }));
  // side は入る側 (in: 上辺) か出る側 (out: 下辺) の決めごと。
  const sides = [
    {
      keyOf: (e) => e.after,
      otherOf: (e) => e.before,
      end: (pts) => pts.length - 1,
      next: (pts) => pts.length - 2,
      dy: -height / 2,
      nearest: Math.max,
    },
    { keyOf: (e) => e.before, otherOf: (e) => e.after, end: () => 0, next: () => 1, dy: height / 2, nearest: Math.min },
  ];
  for (const side of sides) {
    const groups = new Map();
    out.forEach((e) => groups.set(side.keyOf(e), [...(groups.get(side.keyOf(e)) ?? []), e]));
    for (const [key, group] of groups) {
      const c = pos.get(key);
      if (c === undefined) continue;
      // approach は辺がノードへ近づいてくる線分 (anchor から、寄せる点の高さか port の高さのノードの中心へ)。
      const approach = (e) => {
        const pts = e.points;
        const bends = pts.length >= 4;
        const next = side.next(pts);
        const anchor = pts[bends ? next + (next - side.end(pts)) : next];
        return { anchor, aimY: bends ? pts[next].y : c.y + side.dy };
      };
      const lines = new Map(group.map((e) => [e, approach(e)]));
      const refY = side.nearest(...[...lines.values()].map((l) => l.anchor.y));
      const xAt = (e) => {
        const { anchor, aimY } = lines.get(e);
        if (aimY === anchor.y) return anchor.x;
        return anchor.x + ((c.x - anchor.x) * (refY - anchor.y)) / (aimY - anchor.y);
      };
      group.sort(
        (a, b) => xAt(a) - xAt(b) || pos.get(side.otherOf(a)).x - pos.get(side.otherOf(b)).x || side.otherOf(a).localeCompare(side.otherOf(b)),
      );
      group.forEach((e, i) => {
        const x = c.x - width / 2 + (width * (i + 1)) / (group.length + 1);
        e.points[side.end(e.points)] = { x, y: c.y + side.dy };
        if (e.points.length >= 4) e.points[side.next(e.points)].x = x;
      });
    }
  }
  return out;
}

// fitEdges は dagre で配置した graph g の大きさを辺の点列まで含めて広げる。dagre の width / height はノードとラベル
// だけで決まり、ノードの外を回る辺が図の外へはみ出す (#405)。左上へはみ出した分はノードと点列を寄せて余白
// (marginX、marginY) を残す。縮めはしない。
export function fitEdges(g, marginX, marginY) {
  const label = g.graph();
  let minX = marginX;
  let minY = marginY;
  let maxX = label.width - marginX;
  let maxY = label.height - marginY;
  for (const e of g.edges()) {
    for (const p of g.edge(e).points) {
      minX = Math.min(minX, p.x);
      minY = Math.min(minY, p.y);
      maxX = Math.max(maxX, p.x);
      maxY = Math.max(maxY, p.y);
    }
  }
  const dx = marginX - minX;
  const dy = marginY - minY;
  if (dx !== 0 || dy !== 0) {
    for (const v of g.nodes()) {
      const n = g.node(v);
      n.x += dx;
      n.y += dy;
    }
    for (const e of g.edges()) {
      for (const p of g.edge(e).points) {
        p.x += dx;
        p.y += dy;
      }
    }
  }
  label.width = maxX - minX + marginX * 2;
  label.height = maxY - minY + marginY * 2;
}
