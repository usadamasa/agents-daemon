import { test } from "node:test";
import assert from "node:assert/strict";
import { spreadPorts } from "../src/ports.js";

const W = 240;
const H = 72;
const line = (from, to) => [from, { x: (from.x + to.x) / 2, y: (from.y + to.y) / 2 }, to];

test("spreadPorts は入ってくる辺の終点を上辺へ、出ていく辺の始点を下辺へ、相手の x の順に等間隔で散らす", () => {
  // L と R が T へ入り、T から A と B へ出る。T の中心は (300, 200)。
  const pos = new Map([
    ["L", { x: 0, y: 0 }],
    ["R", { x: 600, y: 0 }],
    ["T", { x: 300, y: 200 }],
    ["A", { x: 0, y: 400 }],
    ["B", { x: 600, y: 400 }],
  ]);
  const edges = [
    { before: "R", after: "T", points: line({ x: 600, y: 36 }, { x: 300, y: 164 }) },
    { before: "L", after: "T", points: line({ x: 0, y: 36 }, { x: 300, y: 164 }) },
    { before: "T", after: "B", points: line({ x: 300, y: 236 }, { x: 600, y: 364 }) },
    { before: "T", after: "A", points: line({ x: 300, y: 236 }, { x: 0, y: 364 }) },
  ];
  const got = spreadPorts(edges, pos, W, H);
  const left = 300 - W / 2;
  // 入ってくる辺: L (x=0) が左、R (x=600) が右。
  assert.deepEqual(got[1].points.at(-1), { x: left + W / 3, y: 200 - H / 2 });
  assert.deepEqual(got[0].points.at(-1), { x: left + (2 * W) / 3, y: 200 - H / 2 });
  // 出ていく辺: A (x=0) が左、B (x=600) が右。
  assert.deepEqual(got[3].points[0], { x: left + W / 3, y: 200 + H / 2 });
  assert.deepEqual(got[2].points[0], { x: left + (2 * W) / 3, y: 200 + H / 2 });
  // 中の点と、散らさない側の端 (相手が 1 本だけの端は中央) は変えない。
  assert.deepEqual(got[0].points[1], edges[0].points[1]);
  assert.deepEqual(got[0].points[0], { x: 600, y: 36 });
});

// crosses は線分 p1-p2 と q1-q2 が端以外で交わるか。
function crosses(p1, p2, q1, q2) {
  const side = (a, b, c) => Math.sign((b.x - a.x) * (c.y - a.y) - (b.y - a.y) * (c.x - a.x));
  return side(p1, p2, q1) * side(p1, p2, q2) < 0 && side(q1, q2, p1) * side(q1, q2, p2) < 0;
}

// crossingPairs は辺の点列の、末尾 (tail が true) か先頭の 2 区間どうしが交わる辺の組を返す。
function crossingPairs(edges, tail) {
  const ends = edges.map((e) => (tail ? e.points.slice(-3) : e.points.slice(0, 3)));
  const pairs = [];
  for (let i = 0; i < ends.length; i++) {
    for (let j = i + 1; j < ends.length; j++) {
      for (let a = 0; a < 2; a++) {
        for (let b = 0; b < 2; b++) {
          if (crosses(ends[i][a], ends[i][a + 1], ends[j][b], ends[j][b + 1])) pairs.push(`${edges[i].before}>${edges[i].after} × ${edges[j].before}>${edges[j].after}`);
        }
      }
    }
  }
  return pairs;
}

test("spreadPorts は入ってくる辺の port を、近づいてくる線の並びで割り当て、端の手前の点を port の真上へ寄せる (#707)", () => {
  // #707 で再現した点列。別の枠から入る点線の中の点は、行き先の中心 (1928) の近くに寄って並ぶ。
  // 最後の中の点の x の順 (第 1 段、records、dream) に割り当てると、遠くから浅く入る records の点線が
  // すぐ上から入る第 1 段の実線と交わる。送り元の x の順 (records、dream、第 1 段) では dream と第 1 段が交わる。
  const pos = new Map([
    ["records", { x: 860, y: 0 }],
    ["dream", { x: 1456, y: 0 }],
    ["first", { x: 1784, y: 200 }],
    ["T", { x: 1928, y: 400 }],
  ]);
  const into = (x0, y0, bendX) => [{ x: x0, y: y0 + 36 }, { x: x0, y: y0 + 66 }, { x: bendX, y: 334 }, { x: 1928, y: 364 }];
  const edges = [
    { before: "records", after: "T", points: into(860, 0, 1938) },
    { before: "dream", after: "T", points: into(1456, 0, 2005) },
    { before: "first", after: "T", points: into(1784, 200, 1784) },
  ];
  const got = spreadPorts(edges, pos, W, H);
  const ports = got.map((e) => e.points.at(-1).x);
  // 行き先の近くで左を通る線ほど左の port に入り、端の手前の 2 区間は交わらない。最後の区間は真下を向く
  // (行き先を越えて折り返さない)。
  assert.deepEqual(ports, [1868, 1988, 1928]);
  assert.deepEqual(crossingPairs(got, true), []);
  for (const [i, e] of got.entries()) {
    assert.deepEqual(e.points.at(-2), { x: ports[i], y: 334 }, e.before);
    assert.deepEqual(e.points.at(-1), { x: ports[i], y: 400 - H / 2 }, e.before);
  }
  // 送り元の側の点は変えない。
  assert.deepEqual(got[0].points.slice(0, 2), edges[0].points.slice(0, 2));
});

test("spreadPorts は出ていく辺の port を、離れていく線の並びで割り当て、端の次の点を port の真下へ寄せる (#707)", () => {
  const pos = new Map([
    ["S", { x: 1000, y: 0 }],
    ["L", { x: 0, y: 400 }],
    ["R", { x: 2000, y: 400 }],
  ]);
  // 最初の中の点の x の順 (R へ行く辺が左) に割り当てると、2 本が送り元の下で交わる。
  const edges = [
    { before: "S", after: "L", points: [{ x: 1000, y: 36 }, { x: 1050, y: 66 }, { x: 0, y: 334 }, { x: 0, y: 364 }] },
    { before: "S", after: "R", points: [{ x: 1000, y: 36 }, { x: 960, y: 66 }, { x: 2000, y: 334 }, { x: 2000, y: 364 }] },
  ];
  const got = spreadPorts(edges, pos, W, H);
  const left = 1000 - W / 2;
  assert.deepEqual(got[0].points.slice(0, 2), [{ x: left + W / 3, y: H / 2 }, { x: left + W / 3, y: 66 }]);
  assert.deepEqual(got[1].points.slice(0, 2), [{ x: left + (2 * W) / 3, y: H / 2 }, { x: left + (2 * W) / 3, y: 66 }]);
  assert.deepEqual(crossingPairs(got, false), []);
  // 行き先の側の点は変えない。
  assert.deepEqual(got[0].points.slice(2), edges[0].points.slice(2));
});

// polylineCrossings は辺の点列の全区間どうしが交わる辺の組を返す。
function polylineCrossings(edges) {
  const pairs = [];
  for (let i = 0; i < edges.length; i++) {
    for (let j = i + 1; j < edges.length; j++) {
      const p = edges[i].points;
      const q = edges[j].points;
      let hit = false;
      for (let a = 0; a + 1 < p.length; a++) {
        for (let b = 0; b + 1 < q.length; b++) hit ||= crosses(p[a], p[a + 1], q[b], q[b + 1]);
      }
      if (hit) pairs.push(`${edges[i].before}>${edges[i].after} × ${edges[j].before}>${edges[j].after}`);
    }
  }
  return pairs;
}

// mirror は点列と中心の x を axis で折り返す。
const mirror = (axis) => (p) => ({ ...p, x: 2 * axis - p.x });

test("spreadPorts は 1 段下の中の点が行き先と逆の順に並ぶ辺も、その先の進路の順に port を割り当て、交わらせない (#756)", () => {
  // #756 を app.js の dagre で描いた点列。s1 の 2 本の中の点 (y=184) は、左の pr へ行く辺が右 (522)、右の task へ行く辺が
  // 左 (502) に並ぶ。中の点の順に port を割り当てると、2 本が s1 のすぐ下で交わる。
  const pos = new Map([
    ["s1", { x: 512, y: 104 }],
    ["s2", { x: 1024, y: 104 }],
    ["pr", { x: 184, y: 264 }],
    ["task", { x: 629, y: 264 }],
    ["pr42", { x: 1352, y: 264 }],
  ]);
  const path = (from, mid, to) => [
    { x: from, y: 140 },
    { x: from, y: 162 },
    { x: mid, y: 184 },
    { x: to, y: 206 },
    { x: to, y: 228 },
  ];
  const edges = [
    { before: "s1", after: "pr", points: path(512, 522, 184) },
    { before: "s1", after: "task", points: path(512, 502, 629) },
    { before: "s2", after: "task", points: path(1024, 793, 629) },
    { before: "s2", after: "pr42", points: path(1024, 1091, 1352) },
  ];
  for (const [name, axis] of [
    ["そのまま", null],
    ["左右を反転", 800],
  ]) {
    const flip = axis === null ? (p) => p : mirror(axis);
    const posIn = new Map([...pos].map(([k, c]) => [k, flip(c)]));
    const edgesIn = edges.map((e) => ({ ...e, points: e.points.map(flip) }));
    const got = spreadPorts(edgesIn, posIn, W, H);
    assert.deepEqual(polylineCrossings(got), [], name);
    // s1 の port は、左の行き先へ向かう辺が左を取る。
    const s1 = posIn.get("s1").x;
    const toPr = got[0].points[0].x;
    const toTask = got[1].points[0].x;
    assert.ok(axis === null ? toPr < toTask : toPr > toTask, `${name}: pr=${toPr} task=${toTask} (s1=${s1})`);
  }
});

// bends は点列の中の点のうち、前後の区間の向きが変わる点を返す。
function bends(points) {
  return points.slice(1, -1).filter((p, i) => {
    const [a, b] = [points[i], points[i + 2]];
    const cross = (p.x - a.x) * (b.y - p.y) - (p.y - a.y) * (b.x - p.x);
    return Math.abs(cross) > 1e-6 * Math.hypot(p.x - a.x, p.y - a.y) * Math.hypot(b.x - p.x, b.y - p.y);
  });
}

test("spreadPorts は枠をまたぐ辺を、枠の下辺・上辺から pad だけ離れるまで真っすぐ伸ばしてから曲げる", () => {
  // layout.js の dagre で、window の枠 A・B の s1・s2 から、下の枠 C・D・E の pr・task・pr42 へ引いた点列。
  // 端の隣の点 (y=162、y=206) は枠の下辺・上辺に乗り、task へ入る 2 本は #756 の戻しで両者の間を 1 本の斜線で結ぶ。
  const frames = [
    { x: 566, y: 184, width: 1004, height: 320 },
    { x: 338.5, y: 104, width: 415, height: 116 },
    { x: 778.5, y: 104, width: 425, height: 116 },
    { x: 238, y: 264, width: 308, height: 116 },
    { x: 566, y: 264, width: 308, height: 116 },
    { x: 894, y: 264, width: 308, height: 116 },
  ];
  const pos = new Map([
    ["s1", { x: 285, y: 104 }],
    ["s2", { x: 837, y: 104 }],
    ["pr", { x: 238, y: 264 }],
    ["task", { x: 566, y: 264 }],
    ["pr42", { x: 894, y: 264 }],
  ]);
  const path = (...xs) => xs.map((x, i) => ({ x, y: 140 + 22 * i }));
  const edges = [
    { before: "s1", after: "pr", points: path(256, 238, 238, 238, 238) },
    { before: "s1", after: "task", points: path(399, 469, 576, 576, 572) },
    { before: "s2", after: "task", points: path(735, 673, 556, 556, 560) },
    { before: "s2", after: "pr42", points: path(872, 894, 894, 894, 894) },
  ];
  const pad = 13;
  const got = spreadPorts(edges, pos, W, H, { frames, pad });
  for (const e of got) {
    for (const p of bends(e.points)) {
      for (const f of frames) {
        const inside = p.x > f.x - f.width / 2 && p.x < f.x + f.width / 2;
        for (const border of [f.y - f.height / 2, f.y + f.height / 2]) {
          assert.ok(!inside || Math.abs(p.y - border) >= pad, `${e.before}>${e.after}: (${p.x}, ${p.y}) は枠の線 y=${border} に近い`);
        }
      }
    }
    // port から出る区間と port へ入る区間は真っすぐのまま枠の外まで伸びる。
    assert.equal(e.points[1].x, e.points[0].x, `${e.before}>${e.after}`);
    assert.equal(e.points.at(-2).x, e.points.at(-1).x, `${e.before}>${e.after}`);
  }
  assert.deepEqual(polylineCrossings(got), []);
});

test("spreadPorts は中の点が 2 つの辺も、枠の下辺から pad だけ真っすぐ伸ばしてから曲げる", () => {
  // 実アプリで、枠 A の S から別の枠の T へ、枠の下辺 (y=162) の点と T の真上の点の 2 つを経て引かれた辺。
  const frames = [{ x: 300, y: 104, width: 300, height: 116 }];
  const pos = new Map([
    ["S", { x: 300, y: 104 }],
    ["T", { x: 900, y: 242 }],
  ]);
  const edges = [{ before: "S", after: "T", points: [{ x: 300, y: 140 }, { x: 300, y: 162 }, { x: 900, y: 184 }, { x: 900, y: 206 }] }];
  const got = spreadPorts(edges, pos, W, H, { frames, pad: 13 });
  assert.deepEqual(got[0].points[1], { x: 300, y: 175 });
  assert.deepEqual(got[0].points.slice(2), edges[0].points.slice(2));
});

test("spreadPorts は 1 本だけの端を上辺・下辺の中央に置き、元の辺の points を書き換えない", () => {
  const pos = new Map([
    ["S", { x: 100, y: 0 }],
    ["T", { x: 120, y: 200 }],
  ]);
  const points = line({ x: 101, y: 36 }, { x: 119, y: 164 });
  const got = spreadPorts([{ before: "S", after: "T", points }], pos, W, H);
  assert.deepEqual(got[0].points[0], { x: 100, y: 36 });
  assert.deepEqual(got[0].points.at(-1), { x: 120, y: 164 });
  assert.deepEqual(points[0], { x: 101, y: 36 });
});
