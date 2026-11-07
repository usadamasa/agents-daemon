import { test } from "node:test";
import assert from "node:assert/strict";
import { STATE_TONE, emphasis } from "../src/card.js";

test("作業中のノードはカード全体を working で強調する", () => {
  assert.deepEqual(emphasis({ state: "working" }), ["working"]);
});

test("手当ての要る状態は attn と状態の色で強調し、working は付けない", () => {
  assert.deepEqual(emphasis({ state: "permission" }), ["attn", "warn"]);
  assert.deepEqual(emphasis({ state: "question" }), ["attn", "warn"]);
  assert.deepEqual(emphasis({ state: "limit" }), ["attn", "limit"]);
  assert.deepEqual(emphasis({ state: "error" }), ["attn", "err"]);
});

test("ほかの状態はカードを強調しない", () => {
  for (const state of ["done", "unknown", "pending", "completed", "exited", "external"]) {
    assert.deepEqual(emphasis({ state }), [], state);
  }
});

test("丸の色の鍵は状態ごとに 1 つ", () => {
  assert.equal(STATE_TONE.working, "working");
  assert.equal(STATE_TONE.permission, "warn");
  assert.equal(STATE_TONE.pending, undefined);
});
