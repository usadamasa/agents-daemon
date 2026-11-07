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
