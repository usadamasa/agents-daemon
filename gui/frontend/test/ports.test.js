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

test("spreadPorts は入ってくる辺の port を最後の中の点の x の順に割り当て、その点を port の真上へ寄せる (#707)", () => {
  // #707 で再現した点列。別の枠から入る点線の中の点は、行き先の中心 (1928) の近くに寄って並ぶ。
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
  // 中の点が左にある辺ほど左の port に入り、最後の区間は真下を向く (行き先を越えて折り返さない)。
  assert.deepEqual(ports, [1928, 1988, 1868]);
  for (const [i, e] of got.entries()) {
    assert.deepEqual(e.points.at(-2), { x: ports[i], y: 334 }, e.before);
    assert.deepEqual(e.points.at(-1), { x: ports[i], y: 400 - H / 2 }, e.before);
  }
  // 送り元の側の点は変えない。
  assert.deepEqual(got[0].points.slice(0, 2), edges[0].points.slice(0, 2));
});

test("spreadPorts は出ていく辺の port を最初の中の点の x の順に割り当て、その点を port の真下へ寄せる (#707)", () => {
  const pos = new Map([
    ["S", { x: 1000, y: 0 }],
    ["L", { x: 0, y: 400 }],
    ["R", { x: 2000, y: 400 }],
  ]);
  // 相手のノードの x の順 (L が左) と、最初の中の点の x の順 (R へ行く辺が左) が食い違う。
  const edges = [
    { before: "S", after: "L", points: [{ x: 1000, y: 36 }, { x: 1050, y: 66 }, { x: 0, y: 334 }, { x: 0, y: 364 }] },
    { before: "S", after: "R", points: [{ x: 1000, y: 36 }, { x: 960, y: 66 }, { x: 2000, y: 334 }, { x: 2000, y: 364 }] },
  ];
  const got = spreadPorts(edges, pos, W, H);
  const left = 1000 - W / 2;
  assert.deepEqual(got[1].points.slice(0, 2), [{ x: left + W / 3, y: H / 2 }, { x: left + W / 3, y: 66 }]);
  assert.deepEqual(got[0].points.slice(0, 2), [{ x: left + (2 * W) / 3, y: H / 2 }, { x: left + (2 * W) / 3, y: 66 }]);
  // 行き先の側の点は変えない。
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
