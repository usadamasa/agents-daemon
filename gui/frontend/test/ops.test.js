import { test } from "node:test";
import assert from "node:assert/strict";
import { Graph } from "../src/graph.js";
import { canAdopt, canDismiss, canRename, candidates, canTell, destinationLabel, isOwnTask, issueDestinations, pathDestination, peerDestinations, spawnDestinations, spawnLabel, taskDestinations } from "../src/ops.js";

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

test("canDismiss は daemon が書いた依存図の、終了したセッションのタスクと作業だけ", () => {
  const data = { updated_at: "2026-11-06T00:00:00Z" };
  assert.equal(canDismiss(node("t", { live: false }), data), true);
  assert.equal(canDismiss(node("t", { live: false, state: "completed" }), data), true);
  assert.equal(canDismiss(node("sid:s1", { live: false, kind: "session", state: "exited", origin: "prompt" }), data), true);
  assert.equal(canDismiss(node("t", { live: true }), data), false);
  assert.equal(canDismiss(node("sid:s1", { live: true, kind: "session" }), data), false);
  assert.equal(canDismiss(node("t", { live: false, kind: "todo" }), data), false);
  assert.equal(canDismiss(node("t", { live: false, kind: "ref" }), data), false);
  assert.equal(canDismiss(node("t", { live: false, session_id: undefined }), data), false);
  assert.equal(canDismiss(node("t", { live: false }), {}), false);
});

test("canAdopt は外せるタスクと作業のうち未完了のものだけ", () => {
  const data = { updated_at: "2026-11-06T00:00:00Z" };
  assert.equal(canAdopt(node("t", { live: false }), data), true);
  assert.equal(canAdopt(node("sid:s1", { live: false, kind: "session", state: "exited", origin: "inferred" }), data), true);
  assert.equal(canAdopt(node("t", { live: false, state: "completed" }), data), false);
  assert.equal(canAdopt(node("t", { live: true }), data), false);
});

test("canRename は作業の記録のあるセッションのノードだけ (生きていても終了していても)", () => {
  assert.equal(canRename(node("sid:s1", { kind: "session", origin: "prompt" })), true);
  assert.equal(canRename(node("sid:s1", { kind: "session", origin: "inferred", live: false })), true);
  assert.equal(canRename(node("sid:s1", { kind: "session", origin: "human" })), true);
  assert.equal(canRename(node("sid:s1", { kind: "session", origin: "pane" })), false);
  assert.equal(canRename(node("sid:s1", { kind: "session" })), false);
  assert.equal(canRename(node("t", { origin: "prompt" })), false);
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

test("canTell と isOwnTask は生きているセッションのノードだけ", () => {
  const live = { kind: "task", live: true, session_id: "s1", state: "pending" };
  assert.equal(canTell(live), true);
  assert.equal(canTell({ ...live, live: false }), false);
  assert.equal(canTell({ ...live, session_id: "" }), false);
  assert.equal(isOwnTask(live), true);
  assert.equal(isOwnTask({ ...live, state: "completed" }), false);
  assert.equal(isOwnTask({ ...live, kind: "session" }), false);
});

test("宛先はメモの repo のセッション、ほかの repo、持ち主を除く、の順と重複除去", () => {
  const a = { session_id: "s1", session: "a", subject: "lint", live: true };
  const b = { session_id: "s2", session: "b", subject: "", live: true };
  const gone = { session_id: "s3", session: "c", subject: "", live: false };
  const data = { repos: [{ repo: "/r/x", sessions: [a, gone] }, { repo: "/r/y", sessions: [b, a] }] };
  assert.deepEqual(taskDestinations(data.repos[1]).map((t) => t.session_id), ["s2", "s1"]);
  assert.deepEqual(issueDestinations(data, data.repos[1]).map((t) => t.session_id), ["s2", "s1"]);
  assert.deepEqual(issueDestinations(data, data.repos[0]).map((t) => t.session_id), ["s1", "s2"]);
  assert.deepEqual(peerDestinations(data, "s1").map((t) => t.session_id), ["s2"]);
  assert.equal(destinationLabel(taskDestinations(data.repos[0])[0]), "a (lint)");
  assert.equal(destinationLabel({ route: "peer", session_id: "s2", session: "b", subject: "" }), "b");
  assert.equal(destinationLabel({ route: "spawn", repo: "/r/x", title: "x" }), "x");
});

test("pathDestination は絶対パスだけを受ける", () => {
  assert.deepEqual(pathDestination(" /r/x/ "), { route: "spawn", repo: "/r/x/", title: "x" });
  assert.equal("failure" in pathDestination("r/x"), true);
});
