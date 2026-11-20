import { test } from "node:test";
import assert from "node:assert/strict";
import { GROUP_W, fitLimit, frameTree, groupByRex, groupHeight, heads, partition, placeGroups } from "../src/column.js";

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

const rex = (session, window, label = window) => ({ session_id: session, session_label: `${session}-label`, window_id: window, window_label: label });
// shape は枠を見出し、小見出しごとのカードの key で返す。
const shape = (groups) => groups.map((g) => ({ label: g.label, parts: g.parts.map((p) => [p.label, keys(p.nodes)]) }));

test("groupByRex は rex の session ごとに最初に現れた順で枠にし、中を window ごとに最初に現れた順で区切る", () => {
  const nodes = [
    { key: "b1", kind: "task", session_id: "sb", session: "beta", rex: rex("S2", "w3") },
    { key: "a1", kind: "task", session_id: "sa", session: "alpha", rex: rex("S1", "w1") },
    { key: "c1", kind: "task", session_id: "sc", session: "gamma", rex: rex("S1", "w2") },
    { key: "a2", kind: "task", session_id: "sa", session: "alpha", rex: rex("S1", "w1") },
  ];
  assert.deepEqual(shape(groupByRex(nodes)), [
    { label: "S2-label", parts: [["w3", ["b1"]]] },
    { label: "S1-label", parts: [["w1", ["a1", "a2"]], ["w2", ["c1"]]] },
  ]);
  assert.deepEqual(keys(groupByRex(nodes)[1].nodes), ["a1", "a2", "c1"]);
});

test("groupByRex は rex の外の生きているノードを 1 つの枠にまとめ、中を Claude のセッションで区切る (同じ名前でも session_id で分ける)", () => {
  const groups = groupByRex([
    { key: "a", kind: "task", session_id: "s1", session: "same" },
    { key: "r", kind: "task", session_id: "s3", session: "other", rex: rex("S1", "w1") },
    { key: "b", kind: "task", session_id: "s2", session: "same" },
    { key: "c", kind: "task", session_id: "s1", session: "same" },
  ]);
  assert.deepEqual(shape(groups), [
    { label: "Outside rex", parts: [["same", ["a", "c"]], ["same", ["b"]]] },
    { label: "S1-label", parts: [["w1", ["r"]]] },
  ]);
});

test("groupByRex は window に置かれていない block のノードを小見出しの無い区切りにする", () => {
  const groups = groupByRex([{ key: "d", kind: "task", session_id: "s1", session: "x", rex: { session_id: "S1", session_label: "work" } }]);
  assert.deepEqual(shape(groups), [{ label: "work", parts: [["", ["d"]]] }]);
  assert.equal(heads(groups[0]), 0);
});

test("groupByRex はセッションを持たない参照を「参照」の枠にまとめ、小見出しを付けない", () => {
  const groups = groupByRex([
    { key: "pr:1", kind: "ref" },
    { key: "issue:2", kind: "ref" },
  ]);
  assert.deepEqual(shape(groups), [{ label: "Refs", parts: [["", ["pr:1", "issue:2"]]] }]);
});

// tree は frameTree の枠を key → [見出し, 親の key] で、ノードを key → 親の枠の key で返す。
const tree = ({ frames, parentOf }) => ({
  frames: Object.fromEntries(frames.map((f) => [f.key, [f.label, f.parent ?? null]])),
  parents: Object.fromEntries(parentOf),
});

test("frameTree は辺のある生きているノードを rex の window の枠に、window の枠を session の枠に入れる", () => {
  const linked = [
    { key: "a", kind: "task", state: "working", session_id: "sa", session: "alpha", rex: rex("S1", "w1", "alpha-tab") },
    { key: "b", kind: "task", state: "pending", session_id: "sb", session: "beta", rex: rex("S1", "w2", "beta-tab") },
  ];
  const t = frameTree(linked, []);
  const w1 = t.parentOf.get("a");
  const w2 = t.parentOf.get("b");
  assert.notEqual(w1, w2);
  const { frames } = tree(t);
  assert.deepEqual(frames[w1], ["alpha-tab", t.frames.find((f) => f.label === "S1-label").key]);
  assert.deepEqual(frames[w2], ["beta-tab", frames[w1][1]]);
  assert.deepEqual(frames[frames[w1][1]], ["S1-label", null]);
  // 枠の鍵はノードの key と重ならない。
  for (const f of t.frames) assert.ok(!linked.some((n) => n.key === f.key), f.key);
  assert.deepEqual(keys(t.nodes), ["a", "b"]);
});

test("frameTree は参照と終わったノードを枠に入れない", () => {
  const linked = [
    { key: "pr:o/r#1", kind: "ref", state: "waiting" },
    { key: "x", kind: "task", state: "exited", session_id: "sx", session: "x", rex: rex("S1", "w1") },
    { key: "y", kind: "task", state: "completed", session_id: "sy", session: "y", rex: rex("S1", "w1") },
  ];
  const t = frameTree(linked, []);
  assert.deepEqual(t.frames, []);
  assert.equal(t.parentOf.size, 0);
  assert.deepEqual(keys(t.nodes), ["pr:o/r#1", "x", "y"]);
});

test("frameTree は辺のあるノードを持つ枠へ、同じ枠の辺の無いノードも入れ、ほかの枠の辺の無いノードは free に残す", () => {
  const linked = [{ key: "a", kind: "task", state: "working", session_id: "sa", session: "alpha", rex: rex("S1", "w1") }];
  const free = [
    { key: "f1", kind: "task", state: "pending", session_id: "sz", session: "zeta", rex: rex("S2", "w9") },
    { key: "f2", kind: "session", state: "working", session_id: "sc", session: "gamma", rex: rex("S1", "w2") },
    { key: "f3", kind: "task", state: "pending", session_id: "so", session: "out" },
  ];
  const t = frameTree(linked, free);
  assert.deepEqual(keys(t.nodes), ["a", "f2"]);
  assert.deepEqual(keys(t.free), ["f1", "f3"]);
  const { frames, parents } = tree(t);
  assert.deepEqual(frames[parents.f2], ["w2", frames[parents.a][1]]);
});

test("frameTree は小見出しの無い区切り (window に置かれていない block) のノードを session の枠へ直に入れる", () => {
  const t = frameTree([{ key: "d", kind: "task", state: "working", session_id: "s1", session: "x", rex: { session_id: "S1", session_label: "work" } }], []);
  assert.deepEqual(tree(t), { frames: { [t.parentOf.get("d")]: ["work", null] }, parents: { d: t.parentOf.get("d") } });
});

test("frameTree は rex の外の辺のある生きているノードを「rex の外」の枠に、Claude のセッションの区切りで入れる", () => {
  const t = frameTree([{ key: "o", kind: "session", state: "working", session_id: "so", session: "outside" }], []);
  const { frames, parents } = tree(t);
  assert.deepEqual(frames[parents.o], ["outside", frames[parents.o][1]]);
  assert.deepEqual(frames[frames[parents.o][1]], ["Outside rex", null]);
});

test("groupHeight は枠の中のカードと小見出しの数で決まる", () => {
  assert.equal(groupHeight(1), 124);
  assert.equal(groupHeight(3), 288);
  assert.equal(groupHeight(3, 2), 288 + 2 * 20);
});

test("placeGroups は小見出しの分だけ枠を高くして次の行を下げる", () => {
  const withHeads = { nodes: [{ key: "a" }, { key: "b" }], parts: [{ label: "w1", nodes: [{ key: "a" }] }, { label: "w2", nodes: [{ key: "b" }] }] };
  const { places } = placeGroups([withHeads, { nodes: [{ key: "c" }] }], 64, 24, 100);
  assert.equal(places[1].y, 24 + groupHeight(2, 2) + 16);
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
