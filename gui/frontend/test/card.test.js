import { test } from "node:test";
import assert from "node:assert/strict";
import { STATE_LABEL, STATE_TONE, cardText, emphasis } from "../src/card.js";

test("線でつながるカードの副題はセッション名、状態、今の作業の順", () => {
  const n = { title: "API を実装する", session: "alpha", summary: "テストを書いている" };
  assert.deepEqual(cardText(n, "作業中", false), { title: "API を実装する", sub: "alpha · 作業中 · テストを書いている" });
});

test("枠の中のカードの副題は、見出しが持つセッション名を繰り返さない", () => {
  const n = { title: "API を実装する", session: "alpha", summary: "テストを書いている" };
  assert.deepEqual(cardText(n, "作業中", true), { title: "API を実装する", sub: "作業中 · テストを書いている" });
});

test("枠の中で題がセッション名と同じカードは、今の作業を題にして見出しとの重なりを避ける", () => {
  const n = { title: "zeta", session: "zeta", summary: "issue を読んでいる" };
  assert.deepEqual(cardText(n, "作業中", true), { title: "issue を読んでいる", sub: "作業中" });
});

test("枠の中で題がセッション名と同じでも、今の作業が無ければ題はそのまま", () => {
  assert.deepEqual(cardText({ title: "zeta", session: "zeta" }, "返事待ち (idle)", true), { title: "zeta", sub: "返事待ち (idle)" });
});

test("作業中のノードはカード全体を working で強調する", () => {
  assert.deepEqual(emphasis({ state: "working" }), ["working"]);
});

test("手当ての要る状態は attn と状態の色で強調し、working は付けない", () => {
  assert.deepEqual(emphasis({ state: "permission" }), ["attn", "warn"]);
  assert.deepEqual(emphasis({ state: "question" }), ["attn", "warn"]);
  assert.deepEqual(emphasis({ state: "limit" }), ["attn", "limit"]);
  assert.deepEqual(emphasis({ state: "error" }), ["attn", "err"]);
});

test("取りやめの参照 (マージせずに閉じた PR、not planned の issue) は、完了ではなく手当ての要る状態として強調する", () => {
  assert.equal(STATE_LABEL.dropped, "取りやめ");
  assert.deepEqual(emphasis({ state: "dropped" }), ["attn", "drop"]);
  // 質問待ち・permission 待ち (warn) とは別の色にする。
  assert.equal(STATE_TONE.dropped, "drop");
  assert.ok(Object.entries(STATE_TONE).every(([s, tone]) => s === "dropped" || tone !== "drop"));
});

test("open と引けた参照は、まだ引いていない参照と別の語で出す", () => {
  assert.equal(STATE_LABEL.waiting, "open (待ち)");
  assert.notEqual(STATE_LABEL.waiting, STATE_LABEL.external);
});

test("ほかの状態はカードを強調しない", () => {
  for (const state of ["done", "unknown", "pending", "completed", "exited", "external", "waiting"]) {
    assert.deepEqual(emphasis({ state }), [], state);
  }
});

test("丸の色の鍵は状態ごとに 1 つ", () => {
  assert.equal(STATE_TONE.working, "working");
  assert.equal(STATE_TONE.permission, "warn");
  assert.equal(STATE_TONE.pending, undefined);
});
