import { test } from "node:test";
import assert from "node:assert/strict";
import { GROUP_W, fitLimit, groupBySession, groupHeight, partition, placeGroups } from "../src/column.js";

const keys = (nodes) => nodes.map((n) => n.key);

test("partition は辺の端のノードを図に、辺の無い動いているノードを free に、辺の無い終わったノードを ended に分ける", () => {
  const nodes = [
    { key: "A", state: "working" },
    { key: "B", state: "exited" },
    { key: "X", state: "pending" },
    { key: "Y", state: "exited" },
    { key: "Z", state: "completed" },
  ];
  const links = [{ before: "A", after: "B" }];
  const { connected, free, ended } = partition(nodes, links, ["Z", "B", "X", "Y", "A"]);
  assert.deepEqual(keys(connected), ["A", "B"]);
  assert.deepEqual(keys(free), ["X"]);
  assert.deepEqual(keys(ended), ["Z", "Y"]);
});

test("partition は column_order の順に free を並べ、無いノードは受け取った順で後ろに足す (column_order を出さない古いバイナリ)", () => {
  const nodes = [{ key: "X" }, { key: "Y" }, { key: "Z" }];
  assert.deepEqual(keys(partition(nodes, [], undefined).free), ["X", "Y", "Z"]);
  assert.deepEqual(keys(partition(nodes, [], ["Z"]).free), ["Z", "X", "Y"]);
});

test("groupBySession は session_id ごとに最初に現れた順でまとめ、枠の中は受け取った順を保つ", () => {
  const a1 = { key: "a1", kind: "task", session_id: "sa", session: "alpha" };
  const b1 = { key: "b1", kind: "task", session_id: "sb", session: "beta" };
  const a2 = { key: "a2", kind: "task", session_id: "sa", session: "alpha" };
  assert.deepEqual(
    groupBySession([b1, a1, a2]).map((g) => ({ label: g.label, keys: keys(g.nodes) })),
    [{ label: "beta", keys: ["b1"] }, { label: "alpha", keys: ["a1", "a2"] }],
  );
});

test("groupBySession は同じ名前でも session_id が違えば別の枠にする", () => {
  const groups = groupBySession([
    { key: "a", kind: "task", session_id: "s1", session: "same" },
    { key: "b", kind: "task", session_id: "s2", session: "same" },
  ]);
  assert.equal(groups.length, 2);
});

test("groupBySession はセッションを持たない参照を「参照」の枠にまとめる", () => {
  const groups = groupBySession([
    { key: "pr:1", kind: "ref" },
    { key: "issue:2", kind: "ref" },
  ]);
  assert.deepEqual(groups.map((g) => [g.label, keys(g.nodes)]), [["Refs", ["pr:1", "issue:2"]]]);
});

test("groupHeight は枠の中のカードの数で決まる", () => {
  assert.equal(groupHeight(1), 124);
  assert.equal(groupHeight(3), 288);
});

test("placeGroups は枠を左から並べ、limit を超える枠を次の行へ送り、行の高さは一番高い枠に合わせる", () => {
  const group = (n) => ({ nodes: Array.from({ length: n }, (_, i) => ({ key: String(i) })) });
  const { places, right, bottom } = placeGroups([group(1), group(3), group(1)], 64, 24, 780);
  assert.deepEqual(places.map((p) => p.x), [64, 64 + GROUP_W + 16, 64]);
  assert.deepEqual(places.map((p) => p.y), [24, 24, 24 + groupHeight(3) + 16]);
  assert.equal(right, 64 + GROUP_W + 16 + GROUP_W);
  assert.equal(bottom, 24 + groupHeight(3) + 16 + groupHeight(1));
});

// columns は limit で並べたときの 1 行目の枠の数。
const single = (n) => Array.from({ length: n }, (_, i) => ({ nodes: [{ key: String(i) }] }));
function columns(groups, limit) {
  return placeGroups(groups, 64, 24, limit).places.filter((p) => p.y === 24).length;
}

test("fitLimit は縦に長い見える領域で、全体表示の拡大率がいちばん大きくなる列の数で折り返す", () => {
  const groups = single(4);
  const limit = fitLimit(groups, { left: 64, top: 24, bottom: 24, graph: { width: 0, height: 0 }, view: { width: 600, height: 432 } });
  // 1 列は縦に、3 列以上は横にはみ出して小さくなる。2 列 (676 × 312) が 600 / 676 でいちばん大きい。
  assert.equal(columns(groups, limit), 2);
});

test("fitLimit は 100% のまま収まるなら、横に並べられるだけ並べて行を減らす", () => {
  const groups = single(4);
  const limit = fitLimit(groups, { left: 64, top: 24, bottom: 24, graph: { width: 0, height: 0 }, view: { width: 2000, height: 800 } });
  assert.equal(columns(groups, limit), 4);
});

test("fitLimit は線のある図の方が広ければ、その幅に収まるだけ横に並べる", () => {
  const groups = single(4);
  const limit = fitLimit(groups, { left: 64, top: 300, bottom: 24, graph: { width: 1500, height: 300 }, view: { width: 600, height: 432 } });
  assert.equal(placeGroups(groups, 64, 300, limit).places.filter((p) => p.y === 300).length, 4);
});

test("fitLimit は下限の拡大率でも収まらない図でも、縦横の釣り合う列の数を選び、横 1 行に並べない", () => {
  const groups = single(6);
  const limit = fitLimit(groups, { left: 64, top: 24, bottom: 24, graph: { width: 0, height: 0 }, view: { width: 150, height: 100 } });
  // どの列の数も下限の 30% で止まる。下限で切った値を比べると全部が並び、横 1 行 (6 列) を選んでしまう。
  assert.ok(columns(groups, limit) < 6, `列の数 ${columns(groups, limit)}`);
});

test("placeGroups は limit より広い枠でも 1 つは行に置く", () => {
  const { places } = placeGroups([{ nodes: [{ key: "a" }] }, { nodes: [{ key: "b" }] }], 64, 24, 100);
  assert.deepEqual(places.map((p) => p.x), [64, 64]);
  assert.equal(places[1].y, 24 + groupHeight(1) + 16);
});
