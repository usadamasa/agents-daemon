import { test } from "node:test";
import assert from "node:assert/strict";
import { apply, canSwitch, capture } from "../src/restore.js";

function views(store) {
  return {
    selected: { get: () => store.selected, set: (v) => { store.selected = v; } },
    showCompleted: { get: () => store.showCompleted, set: (v) => { store.showCompleted = v; } },
  };
}

test("capture は登録した鍵ごとに今の値を取り出す", () => {
  const store = { selected: { type: "memo", id: "m1" }, showCompleted: true };
  assert.deepEqual(capture(views(store)), { selected: { type: "memo", id: "m1" }, showCompleted: true });
});

test("apply は保存した値を登録した鍵へ戻し、知らない鍵は飛ばす", () => {
  const store = { selected: null, showCompleted: false };
  apply(views(store), { selected: { type: "node", key: "s/1" }, zoom: 2 });
  assert.deepEqual(store, { selected: { type: "node", key: "s/1" }, showCompleted: false });
});

test("apply は保存が無ければ何もしない", () => {
  const store = { selected: null, showCompleted: false };
  apply(views(store), null);
  assert.deepEqual(store, { selected: null, showCompleted: false });
});

test("apply は 1 つの鍵の set が投げても残りを戻す", () => {
  const store = { showCompleted: false };
  const v = {
    broken: { get: () => 0, set: () => { throw new Error("x"); } },
    showCompleted: { get: () => store.showCompleted, set: (x) => { store.showCompleted = x; } },
  };
  apply(v, { broken: 1, showCompleted: true });
  assert.equal(store.showCompleted, true);
});

test("canSwitch は操作中、打ちかけの文、入力欄の focus があれば待つ", () => {
  const idle = { busy: false, draft: "", note: "", typing: false };
  assert.equal(canSwitch(idle), true);
  assert.equal(canSwitch({ ...idle, busy: true }), false);
  assert.equal(canSwitch({ ...idle, draft: "書きかけ" }), false);
  assert.equal(canSwitch({ ...idle, note: "補足" }), false);
  assert.equal(canSwitch({ ...idle, typing: true }), false);
});
