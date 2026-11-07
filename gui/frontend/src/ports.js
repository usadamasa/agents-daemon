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
