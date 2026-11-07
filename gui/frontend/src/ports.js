// 辺の端をノードの辺に散らす。dagre は辺をノードの中心へ向けるので、同じノードに入る (出る) 辺の端が境界の 1 点に
// 集まり、矢じりが重なって本数が読めない。入ってくる辺の終点は上辺へ、出ていく辺の始点は下辺へ、相手のノードの x の
// 順に等間隔で置く (rankdir TB の前提)。

// spreadPorts は edges ({ before, after, points }) の points の端を置き直した写しを返す。pos は key → ノードの中心。
// 中の点は変えない。
export function spreadPorts(edges, pos, width, height) {
  const out = edges.map((e) => ({ ...e, points: e.points.map((p) => ({ ...p })) }));
  const place = (sideOf, otherOf, pointIndex, dy) => {
    const groups = new Map();
    out.forEach((e) => groups.set(sideOf(e), [...(groups.get(sideOf(e)) ?? []), e]));
    for (const [key, group] of groups) {
      const c = pos.get(key);
      if (c === undefined) continue;
      group.sort((a, b) => pos.get(otherOf(a)).x - pos.get(otherOf(b)).x || otherOf(a).localeCompare(otherOf(b)));
      group.forEach((e, i) => {
        const at = pointIndex(e.points);
        e.points[at] = { x: c.x - width / 2 + (width * (i + 1)) / (group.length + 1), y: c.y + dy };
      });
    }
  };
  place((e) => e.after, (e) => e.before, (pts) => pts.length - 1, -height / 2);
  place((e) => e.before, (e) => e.after, () => 0, height / 2);
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
