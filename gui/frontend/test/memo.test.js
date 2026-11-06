import { test } from "node:test";
import assert from "node:assert/strict";
import { isStale, kindLabel, lastPlacedAt, stamp } from "../src/memo.js";

const memo = (sources, extra = {}) => ({ id: "m1", kind: "aside", origin: "placed", title: "t", sources, ...extra });

test("stamp は RFC 3339 を JST の MM/DD HH:MM にし、読めない文字列はそのまま返す", () => {
  assert.equal(stamp("2026-11-05T05:20:00Z"), "11/05 14:20");
  assert.equal(stamp("2026-11-05T23:30:00+09:00"), "11/05 23:30");
  assert.equal(stamp("not a time"), "not a time");
});

test("lastPlacedAt は sources の先頭 (新しい順) の at、無ければ空", () => {
  assert.equal(lastPlacedAt(memo([{ at: "2026-11-05T05:20:00Z" }, { at: "2026-10-01T00:00:00Z" }])), "2026-11-05T05:20:00Z");
  assert.equal(lastPlacedAt(memo([])), "");
});

test("isStale は最後に置かれてから days 日より経ったか", () => {
  const now = Date.parse("2026-11-20T00:00:00Z");
  assert.equal(isStale(memo([{ at: "2026-11-05T00:00:00Z" }]), now, 14), true);
  assert.equal(isStale(memo([{ at: "2026-11-06T00:00:01Z" }]), now, 14), false);
  assert.equal(isStale(memo([]), now, 14), false);
});

test("kindLabel は adopted のときだけ添える", () => {
  assert.equal(kindLabel(memo([], { kind: "scope" })), "scope");
  assert.equal(kindLabel(memo([], { kind: "scope", origin: "adopted" })), "scope・adopted");
});
