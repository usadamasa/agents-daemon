import { test } from "node:test";
import assert from "node:assert/strict";
import { GROUP_W, groupBySession, groupHeight, partition, placeGroups } from "../src/column.js";

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
  assert.deepEqual(groups.map((g) => [g.label, keys(g.nodes)]), [["参照", ["pr:1", "issue:2"]]]);
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

test("placeGroups は limit より広い枠でも 1 つは行に置く", () => {
  const { places } = placeGroups([{ nodes: [{ key: "a" }] }, { nodes: [{ key: "b" }] }], 64, 24, 100);
  assert.deepEqual(places.map((p) => p.x), [64, 64]);
  assert.equal(places[1].y, 24 + groupHeight(1) + 16);
});
