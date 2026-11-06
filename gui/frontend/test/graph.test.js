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

test("node は key で引き、無ければ undefined", () => {
  const g = new Graph(data);
  assert.equal(g.node("B").key, "B");
  assert.equal(g.node("Z"), undefined);
});
