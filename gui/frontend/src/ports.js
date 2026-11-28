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
//
// 寄せる点の 1 つ手前の点 (kink) がどちらの端の隣でもない辺では、dagre が群れの kink を行き先と逆の順に置くことがあり、
// kink の順に port を割り当てても kink を通る区間がノードの下で交わる (#756)。そこで anchor を kink のさらに 1 つ
// 手前の点にとって port を割り当て、port の順に kink の x が並ばない群れは kink を両隣の点を結ぶ線の上へ戻す。

// spreadPorts は edges ({ before, after, points }) の points の端を置き直した写しを返す。pos は key → ノードの中心。
// 端の隣の点は、両端の間に中の点が 2 つ以上あるときだけ動かす。中の点が 1 つの辺 (隣りの段どうし、待ちの輪の辺) は
// その点を両端が共有するので動かさない。frames は枠の矩形 ({ x, y, width, height }、中心と大きさ) で、pad は
// 枠をまたぐ辺を枠の線から真っすぐ伸ばす長さ (clearFrames)。
export function spreadPorts(edges, pos, width, height, { frames = [], pad = 0 } = {}) {
  const out = edges.map((e) => ({ ...e, points: e.points.map((p) => ({ ...p })) }));
  // straighten は両隣の点を結ぶ線の上へ戻す [辺, 点の添字]。
  const straighten = [];
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
      // kink は戻せる kink の添字 (点が 5 つ以上の辺だけ。ほかは -1)。
      const approach = (e) => {
        const pts = e.points;
        const next = side.next(pts);
        if (pts.length < 4) return { anchor: pts[next], aimY: c.y + side.dy };
        const step = next - side.end(pts);
        const kink = next + step;
        const free = pts.length >= 5;
        return { anchor: pts[free ? kink + step : kink], aimY: pts[next].y, kink: free ? kink : -1 };
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
      const kinks = group.map((e) => e.points[lines.get(e).kink ?? -1]?.x);
      if (kinks.some((x, i) => i > 0 && x !== undefined && kinks.slice(0, i).some((y) => y !== undefined && y > x))) {
        for (const e of group) if (lines.get(e).kink >= 0) straighten.push([e, lines.get(e).kink]);
      }
    }
  }
  clearFrames(out, straighten, frames, pad);
  // 両隣の点は 2 つの側の port を寄せた後に決まるので、戻すのは最後に行う。
  for (const [e, i] of straighten) {
    const [a, p, b] = e.points.slice(i - 1, i + 2);
    if (a.y !== b.y) p.x = a.x + ((b.x - a.x) * (p.y - a.y)) / (b.y - a.y);
  }
  return out;
}

// clearFrames は枠の下辺に乗る端の次の点を下へ、上辺に乗る端の手前の点を上へ pad だけ動かす。dagre は枠の境界を
// 段に置くので、枠をまたぐ辺の端の隣の点は枠の線に乗り、port からの真っすぐの区間が枠の線の直後で曲がって見える。
// 動かす幅は、外側の隣の点 (後で戻す kink は飛ばす) までの斜線が元の 4 割より短くならないよう、その点までの 6 割
// (その点も動かすなら 3 割ずつ) に抑える。
function clearFrames(edges, straighten, frames, pad) {
  if (pad <= 0 || frames.length === 0) return;
  const kinks = new Map();
  for (const [e, i] of straighten) kinks.set(e, new Set([...(kinks.get(e) ?? []), i]));
  const near = (a, b) => Math.abs(a - b) < 0.5;
  for (const e of edges) {
    const pts = e.points;
    if (pts.length < 4) continue;
    const onBorder = (i, dir) => {
      const p = pts[i];
      return frames.some((f) => Math.abs(p.x - f.x) <= f.width / 2 && near(p.y, f.y + (dir * f.height) / 2));
    };
    const ends = [
      { i: 1, dir: 1 },
      { i: pts.length - 2, dir: -1 },
    ].filter((c) => onBorder(c.i, c.dir));
    const shifts = ends.map(({ i, dir }) => {
      let j = i + dir;
      while (kinks.get(e)?.has(j)) j += dir;
      const share = ends.some((c) => c.i === j) ? 0.3 : 0.6;
      return Math.min(pad, Math.abs(pts[j].y - pts[i].y) * share);
    });
    ends.forEach(({ i, dir }, k) => {
      pts[i].y += dir * shifts[k];
    });
  }
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
