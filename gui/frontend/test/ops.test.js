import { test } from "node:test";
import assert from "node:assert/strict";
import { Graph } from "../src/graph.js";
import { canAdopt, canDismiss, candidates, pathDestination, spawnDestinations, spawnLabel, taskText } from "../src/ops.js";

const node = (key, extra = {}) => ({ key, kind: "task", state: "pending", title: key, live: true, depth: 0, downstream: 0, parked: 0, memos: [], blocked_by: [], deadlock: false, halted: false, stuck: false, endpoint: true, session_id: "s1", ...extra });

test("candidates は端点にできるノードから自分、上流、下流、完了、終了を除く", () => {
  // A→B→C と D、E (完了)、F (端点にできない)、G (終了)。
  const g = new Graph({
    nodes: [node("A"), node("B"), node("C"), node("D"), node("E", { state: "completed" }), node("F", { endpoint: false }), node("G", { state: "exited" })],
    links: [
      { before: "A", after: "B", kind: "task" },
      { before: "B", after: "C", kind: "task" },
    ],
  });
  assert.deepEqual(candidates(g, g.node("B")).map((n) => n.key), ["D"]);
  assert.deepEqual(candidates(g, g.node("D")).map((n) => n.key), ["A", "B", "C"]);
});

test("canDismiss は daemon が書いた依存図の、終了したセッションのタスクだけ", () => {
  const data = { updated_at: "2026-11-06T00:00:00Z" };
  assert.equal(canDismiss(node("t", { live: false }), data), true);
  assert.equal(canDismiss(node("t", { live: false, state: "completed" }), data), true);
  assert.equal(canDismiss(node("t", { live: true }), data), false);
  assert.equal(canDismiss(node("t", { live: false, kind: "todo" }), data), false);
  assert.equal(canDismiss(node("t", { live: false, session_id: undefined }), data), false);
  assert.equal(canDismiss(node("t", { live: false }), {}), false);
});

test("canAdopt は外せるタスクのうち未完了のものだけ", () => {
  const data = { updated_at: "2026-11-06T00:00:00Z" };
  assert.equal(canAdopt(node("t", { live: false }), data), true);
  assert.equal(canAdopt(node("t", { live: false, state: "completed" }), data), false);
  assert.equal(canAdopt(node("t", { live: true }), data), false);
});

test("spawnDestinations はメモの repo、依存図の repo、台帳の最近の repo の順で重複を除く", () => {
  const data = {
    repos: [
      { repo: "/r/a", title: "a" },
      { repo: "/r/b", title: "b" },
    ],
    recent_repos: [
      { repo: "/r/c", title: "c", last_seen: "2026-11-06T00:00:00Z" },
      { repo: "/r/a", title: "a", last_seen: "2026-11-05T00:00:00Z" },
    ],
  };
  const out = spawnDestinations(data, { repo: "/r/b", title: "b" });
  assert.deepEqual(out.map((d) => d.repo), ["/r/b", "/r/a", "/r/c"]);
  assert.equal(out[1].last_seen, "2026-11-05T00:00:00Z");
  assert.equal(out[0].last_seen, undefined);
});

test("spawnLabel は最後に見た時刻からの経過を添える", () => {
  const now = Date.parse("2026-11-06T12:00:00Z");
  assert.equal(spawnLabel({ route: "spawn", repo: "/r/a", title: "a" }, now), "a");
  assert.equal(spawnLabel({ route: "spawn", repo: "/r/a", title: "a", last_seen: "2026-11-06T11:30:00Z" }, now), "a (1 時間以内)");
  assert.equal(spawnLabel({ route: "spawn", repo: "/r/a", title: "a", last_seen: "2026-11-06T09:00:00Z" }, now), "a (3 時間前)");
  assert.equal(spawnLabel({ route: "spawn", repo: "/r/a", title: "a", last_seen: "2026-11-01T09:00:00Z" }, now), "a (5 日前)");
});

test("pathDestination は絶対パスだけを受ける", () => {
  assert.deepEqual(pathDestination(" /r/x/ "), { route: "spawn", repo: "/r/x/", title: "x" });
  assert.equal("failure" in pathDestination("r/x"), true);
});

test("taskText はメモの id と前提の確かめを含み、補足を末尾に足す", () => {
  const memo = { id: "m1", kind: "scope", origin: "placed", title: "T", detail: "D", files: ["a.go"], default_taken: "やらない", sources: [{ session: "s", at: "2026-11-06T00:00:00Z", detail: "補" }] };
  const text = taskText(memo, " 急ぎ ");
  for (const part of ["「T」", "(種類: scope)", "メモの id (m1)", "詳細: D", "補足 (s): 補", "関係するファイル: a.go", "置いたときの既定: やらない", "(メモ m1)", "利用者からの補足: 急ぎ"]) {
    assert.ok(text.includes(part), part);
  }
  assert.ok(!taskText(memo).includes("利用者からの補足"));
});
