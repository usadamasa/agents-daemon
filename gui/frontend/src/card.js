// 依存図のノードのカードの見た目の鍵。DOM に触れず、app.js の nodeCard が class として当てる。

// 丸と chip の色の鍵。無い状態は既定の灰。状態の語は OSC 7501 の state と Task 系ツールの status。
export const STATE_TONE = {
  working: "working",
  question: "warn",
  permission: "warn",
  limit: "limit",
  error: "err",
  exited: "exited",
};

// 人の手当てが要る状態。作業中より目立たせる。
const ATTENTION = new Set(["question", "permission", "limit", "error"]);

// emphasis はカード全体に付ける強調の class。作業中は working、手当ての要る状態は attn と色の鍵、ほかは強調しない。
export function emphasis(n) {
  if (n.state === "working") return ["working"];
  if (ATTENTION.has(n.state)) return ["attn", STATE_TONE[n.state]];
  return [];
}
