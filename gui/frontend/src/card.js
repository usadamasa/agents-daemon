// 依存図のノードのカードの見た目の鍵。DOM に触れず、app.js の nodeCard が class として当てる。

// 状態の語は OSC 7501 の state と Task 系ツールの status。external、waiting、dropped は issue / PR の参照のノードだけが持つ。
export const STATE_LABEL = {
  working: "作業中",
  done: "返事待ち (idle)",
  unknown: "不明",
  question: "質問待ち",
  permission: "permission 待ち",
  error: "エラー",
  limit: "上限待ち",
  pending: "未着手",
  completed: "完了",
  exited: "セッション終了",
  external: "外部 (未取得)",
  waiting: "open (待ち)",
  dropped: "取りやめ",
};

// 丸と chip の色の鍵。無い状態は既定の灰。
export const STATE_TONE = {
  working: "working",
  question: "warn",
  permission: "warn",
  limit: "limit",
  error: "err",
  exited: "exited",
  dropped: "drop",
};

// 人の手当てが要る状態。作業中より目立たせる。取りやめの参照は、待っていた下流を人が付け替えるまで止める。
const ATTENTION = new Set(["question", "permission", "limit", "error", "dropped"]);

// emphasis はカード全体に付ける強調の class。作業中は working、手当ての要る状態は attn と色の鍵、ほかは強調しない。
export function emphasis(n) {
  if (n.state === "working") return ["working"];
  if (ATTENTION.has(n.state)) return ["attn", STATE_TONE[n.state]];
  return [];
}
