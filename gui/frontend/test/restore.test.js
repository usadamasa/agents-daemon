import { test } from "node:test";
import assert from "node:assert/strict";
import { apply, capture, watchUpgrade } from "../src/restore.js";

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

// fakeWails は window.wails の代わり。Events.On の handler を持ち、Call.ByName の呼び出しを記録する。
function fakeWails(fail = false) {
  const handlers = {};
  const calls = [];
  globalThis.window = {
    wails: {
      Events: { On: (name, fn) => { handlers[name] = fn; } },
      Call: {
        ByName: async (method, ...args) => {
          calls.push([method, ...args]);
          if (fail) throw new Error("起こせない");
          return "";
        },
      },
    },
  };
  return { handlers, calls };
}

function fakeButton() {
  const b = { hidden: true, disabled: false, title: "", onclick: null };
  b.click = () => b.onclick?.();
  return b;
}

test("watchUpgrade は新しいバージョンの知らせでボタンを出すだけで、押すまで切り替えない", () => {
  const w = fakeWails();
  const button = fakeButton();
  watchUpgrade(views({ selected: null, showCompleted: false }), button);
  assert.equal(button.hidden, true);

  w.handlers["agent-tasks:upgrade"]();
  assert.equal(button.hidden, false);
  assert.deepEqual(w.calls, []);
});

test("watchUpgrade はボタンを押すと今の画面の状態を Upgrade に渡す", async () => {
  const w = fakeWails();
  const button = fakeButton();
  watchUpgrade(views({ selected: { type: "memo", id: "m1" }, showCompleted: true }), button);
  w.handlers["agent-tasks:upgrade"]();

  await button.click();
  assert.equal(w.calls.length, 1);
  assert.equal(w.calls[0][0], "main.Daemon.Upgrade");
  assert.deepEqual(JSON.parse(w.calls[0][1]), { selected: { type: "memo", id: "m1" }, showCompleted: true });
  assert.equal(button.disabled, true);
});

test("watchUpgrade は切り替えに失敗したらボタンを押せる状態に戻し、理由を title に出す", async () => {
  const w = fakeWails(true);
  const button = fakeButton();
  watchUpgrade(views({ selected: null, showCompleted: false }), button);
  w.handlers["agent-tasks:upgrade"]();
  await button.click();
  assert.equal(button.disabled, false);
  assert.match(button.title, /起こせない/);
});
