// 辺の端をノードの辺に散らす。dagre は辺をノードの中心へ向けるので、同じノードに入る (出る) 辺の端が境界の 1 点に
// 集まり、矢じりが重なって本数が読めない。入ってくる辺の終点は上辺へ、出ていく辺の始点は下辺へ等間隔で置く
// (rankdir TB の前提)。
//
// port は端の隣の点 (端に最も近い中の点) の x の順に割り当て、その点を port の真上・真下へ寄せる。別の枠から入る辺の
// 中の点は、dagre が行き先の近くに edgesep の間隔で詰めて置くので、相手のノードの x の順に割り当てると最後の区間が
// 行き先を越えて折り返し、矢じりが 1 か所に寄る (#707)。

// spreadPorts は edges ({ before, after, points }) の points の端を置き直した写しを返す。pos は key → ノードの中心。
// 端の隣の点は、両端の間に中の点が 2 つ以上あるときだけ動かす。中の点が 1 つの辺 (隣りの段どうし、待ちの輪の辺) は
// その点を両端が共有するので動かさない。
export function spreadPorts(edges, pos, width, height) {
  const out = edges.map((e) => ({ ...e, points: e.points.map((p) => ({ ...p })) }));
  const place = (sideOf, otherOf, endIndex, nextIndex, dy) => {
    const groups = new Map();
    out.forEach((e) => groups.set(sideOf(e), [...(groups.get(sideOf(e)) ?? []), e]));
    for (const [key, group] of groups) {
      const c = pos.get(key);
      if (c === undefined) continue;
      const nextX = (e) => e.points[nextIndex(e.points)].x;
      group.sort(
        (a, b) => nextX(a) - nextX(b) || pos.get(otherOf(a)).x - pos.get(otherOf(b)).x || otherOf(a).localeCompare(otherOf(b)),
      );
      group.forEach((e, i) => {
        const x = c.x - width / 2 + (width * (i + 1)) / (group.length + 1);
        e.points[endIndex(e.points)] = { x, y: c.y + dy };
        if (e.points.length >= 4) e.points[nextIndex(e.points)].x = x;
      });
    }
  };
  place((e) => e.after, (e) => e.before, (pts) => pts.length - 1, (pts) => pts.length - 2, -height / 2);
  place((e) => e.before, (e) => e.after, () => 0, () => 1, height / 2);
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
