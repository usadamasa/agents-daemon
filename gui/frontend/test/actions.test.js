import { test } from "node:test";
import assert from "node:assert/strict";
import { discardStale, dismissAll, handOffToNew } from "../src/actions.js";

const memo = { id: "m1", kind: "aside", origin: "placed", title: "T", sources: [] };
const to = { route: "spawn", repo: "/r/a", title: "a" };

// fakeIO は park と run の呼び出しを記録し、答えを順に返す。
function fakeIO({ park = [], run = [] }) {
  const calls = [];
  const events = [];
  return {
    calls,
    events,
    park: async (command, req) => {
      calls.push([command, req]);
      return park.shift() ?? "";
    },
    run: async (command, req) => {
      calls.push([command, req]);
      return run.shift() ?? { stdout: "{}" };
    },
    logEvent: async (event, fields) => {
      events.push({ event, ...fields });
    },
  };
}

test("handOffToNew は取ってから起こし、起こせなければ open に戻す", async () => {
  const io = fakeIO({ run: [{ failure: "rex が無い" }] });
  const message = await handOffToNew(memo, to, "task", "", io);
  assert.deepEqual(
    io.calls.map(([c, r]) => [c, r.status ?? r.repo]),
    [
      ["park set", "handed"],
      ["spawn", "/r/a"],
      ["park set", "open"],
    ],
  );
  assert.equal(io.calls[0][1].to_repo, "/r/a");
  assert.equal(io.calls[0][1].from, "open");
  assert.equal(io.calls[2][1].from, "handed");
  assert.ok(message.includes("起こせませんでした"));
  assert.ok(message.includes("open に戻しました"));
  assert.equal(io.events[0].delivered, false);
});

test("handOffToNew は取れなければ起こさない", async () => {
  const io = fakeIO({ park: ["status が handed"] });
  const message = await handOffToNew(memo, to, "task", "", io);
  assert.equal(io.calls.length, 1);
  assert.ok(message.includes("取れませんでした"));
});

test("handOffToNew は claude が前景にならなかったメモを戻さない", async () => {
  const io = fakeIO({ run: [{ stdout: JSON.stringify({ session_id: "s9", pane_id: "p1", foreground: false }) }] });
  const message = await handOffToNew(memo, to, "task", "", io);
  assert.deepEqual(
    io.calls.map(([c]) => c),
    ["park set", "spawn"],
  );
  assert.ok(message.includes("handed のまま"));
});

test("handOffToNew は起こせたら渡した事実をログに書き、文面に補足を含める", async () => {
  const io = fakeIO({ run: [{ stdout: JSON.stringify({ session_id: "s9", pane_id: "p1", foreground: true }) }] });
  const message = await handOffToNew(memo, to, "task", "急ぎ", io);
  assert.ok(message.includes("メモを渡しました"));
  assert.ok(io.calls[1][1].prompt.includes("利用者からの補足: 急ぎ"));
  assert.deepEqual(io.events, [{ event: "mod.send", via: "memo", memo: "m1", repo: "/r/a", to: "s9", delivered: true }]);
});

test("dismissAll は 1 つが外せなくても残りを続け、数と最初の理由を返す", async () => {
  const io = fakeIO({ park: ["", "まだ生きている", ""] });
  const message = await dismissAll([{ key: "a" }, { key: "b" }, { key: "c" }], io);
  assert.equal(io.calls.length, 3);
  assert.equal(message, "終了したセッションのタスク 2 つを一覧から外しました (1 つは外せませんでした: まだ生きている)");
});

test("discardStale は捨てた数を返す", async () => {
  const io = fakeIO({ run: [{ stdout: "[{}, {}, {}]" }] });
  const message = await discardStale(14, io);
  assert.equal(io.calls[0][1].days, 14);
  assert.ok(message.startsWith("14 日より前のメモ 3 つを捨てました"));
});
