// 辺どうしの交点に飛び越し線 (line jump) を描く (#409)。交点で線が重なるだけだと、乗り換えか交差かが見分けにくい。
// 跨ぐのは後から描く線 (edges の後ろ)。links は before、after の Key の順に並ぶので、同じ図なら跨ぐ側も変わらない。

const EPS = 1e-9;

const cross = (ax, ay, bx, by) => ax * by - ay * bx;
const dist = (a, b) => Math.hypot(a.x - b.x, a.y - b.y);

// intersect は線分 p1-p2 と q1-q2 の交点。平行か交わらなければ null。
function intersect(p1, p2, q1, q2) {
  const rx = p2.x - p1.x;
  const ry = p2.y - p1.y;
  const sx = q2.x - q1.x;
  const sy = q2.y - q1.y;
  const denom = cross(rx, ry, sx, sy);
  if (Math.abs(denom) < EPS) return null;
  const qpx = q1.x - p1.x;
  const qpy = q1.y - p1.y;
  const t = cross(qpx, qpy, sx, sy) / denom;
  const u = cross(qpx, qpy, rx, ry) / denom;
  if (t < 0 || t > 1 || u < 0 || u > 1) return null;
  return { x: p1.x + t * rx, y: p1.y + t * ry };
}

// findJumps は edges (折れ線の点列の並び) のそれぞれについて、先に描いた線との交点の飛び越し ({ seg, x, y }、seg は
// 何本目の線分か) を線の上の順に返す。どちらかの線の端 (ノードの縁、矢じり) から margin より近い交点と、線分の
// 折れ目から radius より近い交点 (弧が折れ目にかかる) は飛ばし、同じ線分で弧が重なる交点は先のものだけ残す。
// 線分の数を S として O(S^2)。
export function findJumps(edges, { radius, margin }) {
  const nearEnd = (pts, c) => dist(c, pts[0]) < margin || dist(c, pts[pts.length - 1]) < margin;
  return edges.map((pts, i) => {
    const out = [];
    for (let k = 0; k + 1 < pts.length; k++) {
      const a = pts[k];
      const b = pts[k + 1];
      const found = [];
      for (let j = 0; j < i; j++) {
        const other = edges[j];
        for (let m = 0; m + 1 < other.length; m++) {
          const c = intersect(a, b, other[m], other[m + 1]);
          if (c === null || nearEnd(pts, c) || nearEnd(other, c)) continue;
          if (dist(c, a) < radius || dist(c, b) < radius) continue;
          found.push(c);
        }
      }
      found.sort((p, q) => dist(p, a) - dist(q, a));
      let last = null;
      for (const c of found) {
        if (last !== null && dist(c, last) < radius * 2) continue;
        out.push({ seg: k, x: c.x, y: c.y });
        last = c;
      }
    }
    return out;
  });
}

const fmt = (n) => n.toFixed(1);

// jumpPath は点列 points の path の d。jumps (findJumps の 1 本分) の所で線を切り、半径 radius の弧で跨ぐ。
// 弧は線分の進む向きによらず上へ膨らむ (縦の線分は右へ)。
export function jumpPath(points, jumps, radius) {
  let d = `M ${fmt(points[0].x)} ${fmt(points[0].y)}`;
  for (let k = 0; k + 1 < points.length; k++) {
    const a = points[k];
    const b = points[k + 1];
    const len = dist(a, b);
    const ux = (b.x - a.x) / len;
    const uy = (b.y - a.y) / len;
    const sweep = ux >= 0 ? 1 : 0;
    for (const j of jumps) {
      if (j.seg !== k) continue;
      d += ` L ${fmt(j.x - radius * ux)} ${fmt(j.y - radius * uy)}`;
      d += ` A ${radius} ${radius} 0 0 ${sweep} ${fmt(j.x + radius * ux)} ${fmt(j.y + radius * uy)}`;
    }
    d += ` L ${fmt(b.x)} ${fmt(b.y)}`;
  }
  return d;
}
