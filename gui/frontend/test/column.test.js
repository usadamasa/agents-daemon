import { test } from "node:test";
import assert from "node:assert/strict";
import { partition } from "../src/column.js";

const keys = (nodes) => nodes.map((n) => n.key);

test("partition は辺の端のノードを図に、辺の無いノードを列に分け、列は column_order の順にする", () => {
  const nodes = [{ key: "A" }, { key: "B" }, { key: "X" }, { key: "Y" }, { key: "Z" }];
  const links = [{ before: "A", after: "B" }];
  const { connected, column } = partition(nodes, links, ["Z", "B", "X", "Y", "A"]);
  assert.deepEqual(keys(connected), ["A", "B"]);
  assert.deepEqual(keys(column), ["Z", "X", "Y"]);
});

test("partition は column_order に無いノードを受け取った順で後ろに足す (column_order を出さない古いバイナリ)", () => {
  const nodes = [{ key: "X" }, { key: "Y" }, { key: "Z" }];
  assert.deepEqual(keys(partition(nodes, [], undefined).column), ["X", "Y", "Z"]);
  assert.deepEqual(keys(partition(nodes, [], ["Z"]).column), ["Z", "X", "Y"]);
});
