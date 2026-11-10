import { test } from "node:test";
import assert from "node:assert/strict";
import { PR_STAGE, STATE_LABEL, STATE_TONE, acceptanceLine, cardHint, cardText, emphasis, leadStage, toneOf } from "../src/card.js";

test("acceptanceLine は受け入れ条件の行を選び、行の途中の印からを返す", () => {
  const task = (description) => ({ kind: "task", description });
  assert.equal(acceptanceLine(task("背景の説明\n受け入れ条件: CI が緑\n補足")), "受け入れ条件: CI が緑");
  assert.equal(acceptanceLine(task("マージする。受け入れ条件: CI が緑")), "受け入れ条件: CI が緑");
  assert.equal(acceptanceLine(task("- 受け入れ条件：CI が緑")), "受け入れ条件：CI が緑");
});

test("acceptanceLine は印だけの行なら次の行を添える", () => {
  assert.equal(acceptanceLine({ kind: "task", description: "受け入れ条件:\n\n- CI が緑\n- レビュー済み" }), "受け入れ条件: CI が緑");
  assert.equal(acceptanceLine({ kind: "task", description: "説明\n受け入れ条件:" }), "受け入れ条件:");
});

test("acceptanceLine は印が無ければ description の空でない 1 行目を返す", () => {
  assert.equal(acceptanceLine({ kind: "task", description: "\n  PR を確かめる  \n二行目" }), "PR を確かめる");
});

test("acceptanceLine は description が無いタスクとタスクでないカードは空", () => {
  assert.equal(acceptanceLine({ kind: "task" }), "");
  assert.equal(acceptanceLine({ kind: "task", description: "  \n" }), "");
  assert.equal(acceptanceLine({ kind: "session", description: "受け入れ条件: x" }), "");
});

test("cardHint は今の作業と受け入れ条件を行に分け、どちらも無ければ空", () => {
  assert.equal(cardHint({ kind: "task", summary: "テストを書いている", description: "受け入れ条件: CI が緑" }), "テストを書いている\n受け入れ条件: CI が緑");
  assert.equal(cardHint({ kind: "task", description: "PR を確かめる" }), "PR を確かめる");
  assert.equal(cardHint({ kind: "session", summary: "読んでいる" }), "読んでいる");
  assert.equal(cardHint({ kind: "ref" }), "");
});

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
  assert.equal(STATE_LABEL.dropped, "Dropped");
  assert.deepEqual(emphasis({ state: "dropped" }), ["attn", "drop"]);
  // 質問待ち・permission 待ち (warn) とは別の色にする。
  assert.equal(STATE_TONE.dropped, "drop");
  assert.ok(Object.entries(STATE_TONE).every(([s, tone]) => s === "dropped" || tone !== "drop"));
});

test("open と引けた参照は、まだ引いていない参照と別の語で出す", () => {
  assert.equal(STATE_LABEL.waiting, "Open");
  assert.notEqual(STATE_LABEL.waiting, STATE_LABEL.external);
});

test("ほかの状態はカードを強調しない", () => {
  for (const state of ["done", "unknown", "pending", "completed", "exited", "external", "waiting"]) {
    assert.deepEqual(emphasis({ state }), [], state);
  }
});

test("PR の段: カードの見た目は次の手で分ける", () => {
  const lead = (...pr_stages) => emphasis({ state: "waiting", pr_stages });
  // 自分で直す
  for (const s of ["ci_failing", "conflicting", "changes_requested", "behind"]) assert.deepEqual(lead(s), ["attn", "err"], s);
  // 仕上げる
  assert.deepEqual(lead("draft"), ["draft"]);
  // 人の返事を待つ
  assert.deepEqual(lead("review_required"), ["attn", "warn"]);
  // CI を待つ
  assert.deepEqual(lead("ci_running"), ["working", "bounce"]);
  // マージする
  assert.deepEqual(lead("mergeable"), ["attn", "merge"]);
  // 次の手が決まらない段は強調しない
  for (const s of ["ci_passing", "approved", "someday_new"]) assert.deepEqual(lead(s), [], s);
});

test("PR の段: 丸の色は先頭の段で決め、段の無いノードと知らない語は状態の色のまま", () => {
  const tone = (...pr_stages) => toneOf({ state: "waiting", pr_stages });
  assert.equal(tone("ci_failing", "ci_running"), "err");
  assert.equal(tone("conflicting"), "err");
  assert.equal(tone("review_required"), "warn");
  assert.equal(tone("changes_requested"), "err");
  assert.equal(tone("behind"), "err");
  assert.equal(tone("ci_running"), "working");
  // 緑は機械が動いている色なので、人がマージする段には使わない。
  assert.equal(tone("mergeable"), "merge");
  assert.equal(tone("ci_passing"), undefined);
  assert.equal(tone("approved"), undefined);
  // 取りやめの色 (drop) を使わない。
  assert.equal(tone("draft"), undefined);
  assert.equal(tone("someday_new"), STATE_TONE.waiting);
  assert.equal(toneOf({ state: "working" }), "working");
  assert.equal(toneOf({ state: "dropped" }), "drop");
});

test("PR の段: 箱に出す語", () => {
  assert.equal(leadStage({ pr_stages: ["ci_failing", "draft"] }).label, "CI failing");
  assert.equal(leadStage({ pr_stages: [] }), undefined);
  assert.equal(leadStage({}), undefined);
  assert.equal(leadStage({ pr_stages: ["someday_new"] }), undefined);
  const labels = Object.values(PR_STAGE).map((s) => s.label);
  assert.equal(new Set(labels).size, labels.length);
});

test("丸の色の鍵は状態ごとに 1 つ", () => {
  assert.equal(STATE_TONE.working, "working");
  assert.equal(STATE_TONE.permission, "warn");
  assert.equal(STATE_TONE.pending, undefined);
});
