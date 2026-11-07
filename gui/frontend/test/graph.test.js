import { test } from "node:test";
import assert from "node:assert/strict";
import { Graph } from "../src/graph.js";

const node = (key, extra = {}) => ({ key, kind: "task", state: "pending", title: key, live: true, depth: 0, downstream: 0, parked: 0, memos: [], blocked_by: [], deadlock: false, halted: false, stuck: false, endpoint: true, ...extra });

// A→B→C と D→C。
const data = {
  nodes: [node("A"), node("B"), node("C"), node("D")],
  links: [
    { before: "A", after: "B", kind: "task" },
    { before: "B", after: "C", kind: "task" },
    { before: "D", after: "C", kind: "edge" },
  ],
};

test("after は下流の直近、blockers は上流の直近の辺", () => {
  const g = new Graph(data);
  assert.deepEqual(g.after("A").map((n) => n.key), ["B"]);
  assert.deepEqual(g.after("C"), []);
  assert.deepEqual(g.blockers("C").map((l) => `${l.before}:${l.kind}`), ["B:task", "D:edge"]);
});

test("reach と upstream は自分を含めずに輪を止める", () => {
  const g = new Graph({ ...data, links: [...data.links, { before: "C", after: "A", kind: "task" }] });
  assert.deepEqual([...g.reach("A")].sort(), ["B", "C"]);
  assert.deepEqual([...g.upstream("C")].sort(), ["A", "B", "D"]);
});

test("view は完了を隠すとき folded のノードと段、両端が見える辺と畳んだ辺を返す", () => {
  // A→B(完了)→C と D→C、E(完了) は D の末端として残る。
  const g = new Graph({
    nodes: [node("A"), node("B", { state: "completed", depth: 1 }), node("C", { depth: 2 }), node("D"), node("E", { state: "completed", depth: 1 })],
    links: [
      { before: "A", after: "B", kind: "task" },
      { before: "B", after: "C", kind: "task" },
      { before: "D", after: "C", kind: "edge" },
      { before: "D", after: "E", kind: "edge" },
    ],
    folded: {
      nodes: [{ key: "A", depth: 0 }, { key: "C", depth: 1 }, { key: "D", depth: 0 }, { key: "E", depth: 1 }],
      links: [{ before: "A", after: "C", via: ["B"] }],
    },
  });
  const hidden = g.view(false);
  assert.deepEqual(hidden.nodes.map((n) => n.key), ["A", "C", "D", "E"]);
  assert.deepEqual(
    hidden.links.map((l) => `${l.before}>${l.after}:${l.kind}:${(l.via ?? []).join(",")}`),
    ["D>C:edge:", "D>E:edge:", "A>C:folded:B"],
  );
  // 段は畳んだ図で数え直したもの (C は全部の図では 2 段目)。
  assert.deepEqual([...hidden.depth], [["A", 0], ["C", 1], ["D", 0], ["E", 1]]);
  const all = g.view(true);
  assert.equal(all.nodes.length, 5);
  assert.equal(all.links.length, 4);
  assert.equal(all.depth.get("C"), 2);
});

test("view は folded の無い JSON (古いバイナリ) なら完了を落とし、両端が見える辺だけを返す", () => {
  const g = new Graph({
    nodes: [node("A"), node("B", { state: "completed", depth: 1 }), node("C", { depth: 2 })],
    links: [
      { before: "A", after: "B", kind: "task" },
      { before: "B", after: "C", kind: "task" },
      { before: "A", after: "C", kind: "edge" },
    ],
  });
  const hidden = g.view(false);
  assert.deepEqual(hidden.nodes.map((n) => n.key), ["A", "C"]);
  assert.deepEqual(hidden.links.map((l) => `${l.before}>${l.after}`), ["A>C"]);
  assert.equal(hidden.depth.get("C"), 2);
});

test("waiters は key から出ていく辺、settled は下流があって全部完了しているか", () => {
  const g = new Graph({
    nodes: [node("P", { state: "done" }), node("R", { state: "completed" }), node("Q", { state: "done" }), node("S"), node("Z", { state: "done" })],
    links: [
      { before: "P", after: "R", kind: "edge", edge: { id: "e1", note: "報告" } },
      { before: "Q", after: "R", kind: "edge" },
      { before: "Q", after: "S", kind: "edge" },
    ],
  });
  assert.deepEqual(g.waiters("Q").map((l) => l.after), ["R", "S"]);
  assert.equal(g.waiters("P")[0].edge.note, "報告");
  assert.equal(g.settled("P"), true);
  assert.equal(g.settled("Q"), false);
  // 下流の無いノードは、こちらの返事を待っているかもしれないので settled にしない。
  assert.equal(g.settled("Z"), false);
});

test("node は key で引き、無ければ undefined", () => {
  const g = new Graph(data);
  assert.equal(g.node("B").key, "B");
  assert.equal(g.node("Z"), undefined);
});
