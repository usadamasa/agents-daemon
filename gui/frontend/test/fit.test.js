import { test } from "node:test";
import assert from "node:assert/strict";
import { fitEdges } from "../src/ports.js";

// dagre の graph の形 (nodes / node / edges / edge / graph) だけを持つ写し。
function fake(nodes, edges, size) {
  const label = { ...size };
  return {
    nodes: () => [...nodes.keys()],
    node: (v) => nodes.get(v),
    edges: () => edges.map((_, i) => i),
    edge: (i) => edges[i],
    graph: () => label,
  };
}

test("fitEdges は辺の点列がノードの外へ出ていれば、点列まで含めて図を広げて余白を揃える", () => {
  // A (100, 50) から B (100, 250) へ、右へ大きく回る辺。ノードの幅 40、高さ 20、余白 10。
  const nodes = new Map([
    ["A", { x: 100, y: 50, width: 40, height: 20 }],
    ["B", { x: 100, y: 250, width: 40, height: 20 }],
  ]);
  const edge = { points: [{ x: 100, y: 60 }, { x: 400, y: 150 }, { x: -30, y: 200 }, { x: 100, y: 240 }] };
  const g = fake(nodes, [edge], { width: 130, height: 270 });
  fitEdges(g, 10, 10);
  // 左端は点列の -30。余白 10 を残して全体を 40 右へ寄せ、右端は点列の 400 に余白を足す。
  assert.deepEqual(g.node("A"), { x: 140, y: 50, width: 40, height: 20 });
  assert.deepEqual(edge.points[2], { x: 10, y: 200 });
  assert.equal(g.graph().width, 400 - -30 + 20);
  assert.equal(g.graph().height, 270);
});

test("fitEdges は点列がノードの内側に収まっていれば何も変えない", () => {
  const nodes = new Map([["A", { x: 30, y: 20, width: 40, height: 20 }], ["B", { x: 30, y: 120, width: 40, height: 20 }]]);
  const edge = { points: [{ x: 30, y: 30 }, { x: 30, y: 110 }] };
  const g = fake(nodes, [edge], { width: 60, height: 140 });
  fitEdges(g, 10, 10);
  assert.deepEqual(g.node("A"), { x: 30, y: 20, width: 40, height: 20 });
  assert.deepEqual(g.graph(), { width: 60, height: 140 });
});
