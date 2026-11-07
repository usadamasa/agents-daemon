import { test } from "node:test";
import assert from "node:assert/strict";
import { askUnlink, discardStale, dismissAll, handOff, handOffTask, instruct, setStatus, start, toParking } from "../src/actions.js";

const memo = { id: "m1", kind: "aside", origin: "placed", title: "T", sources: [] };
const spawnTo = { route: "spawn", repo: "/r/a", title: "a" };
const peerTo = { route: "peer", session_id: "s2", session: "fix-lint", subject: "" };
const task = { key: "task:s1/3", kind: "task", task_id: "3", title: "lint を直す", description: "閾値を見直す", session: "deps-app", session_id: "s1", live: true, state: "pending", memos: [] };

// fakeIO は park と run の呼び出しを記録し、答えを順に返す。
function fakeIO({ park = [], run = [] }) {
  const calls = [];
  return {
    calls,
    park: async (command, req) => {
      calls.push([command, req]);
      return park.shift() ?? "";
    },
    run: async (command, req) => {
      calls.push([command, req]);
      return run.shift() ?? { stdout: "{}" };
    },
  };
}

function json(result) {
  return { stdout: JSON.stringify(result) };
}

const toldOK = json({ pane_id: "p1" });

test("handOff は park hand を 1 回呼び、宛先と補足と操作主体を渡す", async () => {
  const io = fakeIO({ run: [json({ delivered: true, status: "handed", to: "s2", pane_id: "p1" })] });
  const res = await handOff(memo, peerTo, "issue", "急ぎ", io);
  assert.deepEqual(io.calls, [["park hand", { id: "m1", intent: "issue", note: "急ぎ", to_session: "s2", session_id: "app" }]]);
  assert.deepEqual(res, { ok: true, message: "fix-lint へ送り、メモを渡しました" });
});

test("handOff は新しいセッションへは to_repo で渡し、注意があれば添える", async () => {
  const io = fakeIO({ run: [json({ delivered: true, status: "handed", to: "s9", pane_id: "p1", warning: "検知語" })] });
  const res = await handOff(memo, spawnTo, "task", "", io);
  assert.equal(io.calls[0][1].to_repo, "/r/a");
  assert.deepEqual(res, { ok: true, message: "a で新しいセッションを起こし、メモを渡しました (検知語)" });
});

test("handOff は届かず open に戻ったことを文面にする", async () => {
  const io = fakeIO({ run: [json({ delivered: false, status: "open", to: "s2", reason: "prompt が開いている" })] });
  const res = await handOff(memo, peerTo, "task", "", io);
  assert.deepEqual(res, { ok: false, message: "fix-lint へ送れませんでした: prompt が開いている。メモは open に戻しました" });
});

test("handOff は claude が前景にならなかったメモが handed のまま残ることを文面にする", async () => {
  const io = fakeIO({ run: [json({ delivered: false, status: "handed", to: "s9", pane_id: "p1", reason: "待ち切れた" })] });
  const res = await handOff(memo, spawnTo, "task", "", io);
  assert.equal(res.ok, false);
  assert.ok(res.message.startsWith("a で新しい rex session を作りましたが"));
  assert.ok(res.message.includes("handed のまま"));
});

test("handOff は取れなかった (exit 1) ときに理由を返す", async () => {
  const io = fakeIO({ run: [{ failure: "メモを取れない (ほかが先に渡したか、捨てたかもしれない): status は handed" }] });
  const res = await handOff(memo, peerTo, "task", "", io);
  assert.equal(res.ok, false);
  assert.ok(res.message.startsWith("メモを渡せませんでした: メモを取れない"));
});

test("instruct はタスクの題を添えて tell に渡し、拒まれたら理由を返す", async () => {
  const io = fakeIO({ run: [toldOK, { failure: "入力欄に打ちかけの文がある" }] });
  const res = await instruct(task, "先にテストを書いて", io);
  assert.deepEqual(io.calls[0], ["tell", { to_session: "s1", text: "タスク「lint を直す」について: 先にテストを書いて", session_id: "app" }]);
  assert.deepEqual(res, { ok: true, message: "deps-app の入力欄に打ちました" });
  const refused = await instruct(task, "もう一度", io);
  assert.deepEqual(refused, { ok: false, message: "deps-app へ送れませんでした: 入力欄に打ちかけの文がある" });
});

test("askUnlink は待っている側のセッションへ解消を頼む", async () => {
  const io = fakeIO({ run: [toldOK] });
  const before = { ...task, key: "task:s1/2", task_id: "2", title: "準備" };
  await askUnlink(before, task, io);
  assert.equal(io.calls[0][1].to_session, "s1");
  assert.ok(io.calls[0][1].text.startsWith("タスク #3「lint を直す」が #2「準備」の完了待ち"));
});

test("start と setStatus は持ち主へ TaskUpdate を頼む", async () => {
  const io = fakeIO({ run: [toldOK, toldOK] });
  const started = await start(task, "急ぎ", io);
  assert.ok(io.calls[0][1].text.includes("in_progress にしてから"));
  assert.ok(io.calls[0][1].text.endsWith("利用者からの補足: 急ぎ"));
  assert.equal(started.message, "deps-app に着手を頼みました");
  const done = await setStatus(task, "completed", io);
  assert.ok(io.calls[1][1].text.includes("completed にしてください"));
  assert.equal(done.message, "deps-app に completed を頼みました");
});

test("handOffTask は相手へ送れたら持ち主にタスクを消すよう頼む", async () => {
  const io = fakeIO({ run: [toldOK, toldOK] });
  const res = await handOffTask(task, peerTo, "", io);
  assert.equal(io.calls[0][1].to_session, "s2");
  assert.ok(io.calls[0][1].text.startsWith("deps-app (session ID s1) からタスク「lint を直す」を渡します。"));
  assert.ok(io.calls[0][1].text.includes("詳細: 閾値を見直す"));
  assert.equal(io.calls[1][1].to_session, "s1");
  assert.ok(io.calls[1][1].text.includes("fix-lint へ渡しました"));
  assert.deepEqual(res, { ok: true, message: "fix-lint へ渡し、deps-app にタスクを消すよう頼みました" });
});

test("handOffTask は相手へ送れなければ持ち主に頼まない", async () => {
  const io = fakeIO({ run: [{ failure: "prompt が開いている" }] });
  const res = await handOffTask(task, peerTo, "", io);
  assert.equal(io.calls.length, 1);
  assert.deepEqual(res, { ok: false, message: "fix-lint へ送れませんでした: prompt が開いている" });
});

test("handOffTask は新しいセッションを起こし、前景にならなければタスクを残す", async () => {
  const io = fakeIO({ run: [json({ session_id: "s9", pane_id: "p9", foreground: true }), toldOK] });
  const res = await handOffTask(task, spawnTo, "補足", io);
  assert.equal(io.calls[0][0], "spawn");
  assert.ok(io.calls[0][1].prompt.startsWith("このタスクを渡すため、a (/r/a) で新しいセッションとして起こしました。\n"));
  assert.ok(io.calls[0][1].prompt.endsWith("利用者からの補足: 補足"));
  assert.ok(res.ok && res.message.startsWith("a で新しいセッションを起こして渡し"));

  const io2 = fakeIO({ run: [json({ session_id: "s9", pane_id: "p9", foreground: false, detail: "待ち切れた" })] });
  const res2 = await handOffTask(task, spawnTo, "", io2);
  assert.equal(io2.calls.length, 1);
  assert.ok(!res2.ok && res2.message.includes("タスクは残してあります"));
});

test("toParking は扱うメモが無ければ park tool で置くよう頼む", async () => {
  const io = fakeIO({ run: [toldOK] });
  const res = await toParking(task, io);
  assert.ok(io.calls[0][1].text.includes("park tool で"));
  assert.deepEqual(res, { ok: true, message: "deps-app にメモに置くよう頼みました" });
});

test("toParking は handed のメモを open に戻してから頼み、頼めなくてもメモは open のまま残す", async () => {
  const node = { ...task, memos: [{ id: "m1", title: "a", status: "handed" }, { id: "m2", title: "b", status: "discarded" }, { id: "m3", title: "c", status: "open" }] };
  const io = fakeIO({ run: [toldOK] });
  const res = await toParking(node, io);
  assert.deepEqual(io.calls[0], ["park set", { id: "m1", status: "open", from: "handed" }]);
  assert.ok(io.calls[1][1].text.includes("元のメモ (m1, m3) を"));
  assert.equal(res.message, "メモ m1, m3 を open に戻し、deps-app にタスクを消すよう頼みました");

  // 頼めなくてもメモは一覧に戻っているので取り直さない (#380)。押し直すとタスクを消す依頼だけが送り直される。
  const io2 = fakeIO({ run: [{ failure: "上限の画面" }] });
  const res2 = await toParking(node, io2);
  assert.equal(io2.calls.length, 2);
  assert.deepEqual(res2, {
    ok: false,
    message: "メモ m1, m3 は open に戻しましたが、deps-app へタスクを消す依頼を送れませんでした: 上限の画面。もう一度「メモにする」を押すと依頼だけ送り直します",
  });

  const reopened = { ...node, memos: [{ id: "m1", title: "a", status: "open" }, { id: "m3", title: "c", status: "open" }] };
  const io3 = fakeIO({ run: [toldOK] });
  const res3 = await toParking(reopened, io3);
  assert.equal(io3.calls.length, 1);
  assert.ok(io3.calls[0][1].text.includes("元のメモ (m1, m3) を"));
  assert.ok(res3.ok);
});

test("toParking は扱うメモが無くて頼めなければ、そのまま理由を返す", async () => {
  const io = fakeIO({ run: [{ failure: "上限の画面" }] });
  const res = await toParking(task, io);
  assert.deepEqual(res, { ok: false, message: "deps-app へ送れませんでした: 上限の画面" });
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
