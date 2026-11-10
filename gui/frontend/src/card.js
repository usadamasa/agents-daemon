// 依存図のノードのカードの見た目の鍵。DOM に触れず、app.js の nodeCard が class として当てる。

// 状態の語は OSC 7501 の state と Task 系ツールの status。external、waiting、dropped は issue / PR の参照のノードだけが持つ。
export const STATE_LABEL = {
  working: "Working",
  done: "Awaiting reply",
  unknown: "Unknown",
  question: "Question",
  permission: "Permission",
  error: "Error",
  limit: "Limit",
  pending: "Pending",
  completed: "Completed",
  exited: "Exited",
  external: "External",
  waiting: "Open",
  dropped: "Dropped",
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

// 次の手ごとの見た目。丸と chip の色の鍵と、先頭のときのカードの class。
const FIX = { tone: "err", card: ["attn", "err"] }; // 自分で直す
const FINISH = { card: ["draft"] }; // 仕上げる
const AWAIT_REVIEW = { tone: "warn", card: ["attn", "warn"] }; // 人の返事を待つ
const AWAIT_CI = { tone: "working", card: ["working", "bounce"] }; // CI を待つ
const MERGE = { tone: "merge", card: ["attn", "merge"] }; // マージする
const UNDECIDED = { card: [] }; // 事実だけで、次の手を示さない

// open の PR の段の語と次の手。
export const PR_STAGE = {
  ci_failing: { label: "CI failing", ...FIX },
  conflicting: { label: "Conflicts", ...FIX },
  draft: { label: "Draft", ...FINISH },
  changes_requested: { label: "Changes requested", ...FIX },
  review_required: { label: "Review required", ...AWAIT_REVIEW },
  ci_running: { label: "CI running", ...AWAIT_CI },
  behind: { label: "Behind base", ...FIX },
  mergeable: { label: "Mergeable", ...MERGE },
  ci_passing: { label: "CI passing", ...UNDECIDED },
  approved: { label: "Approved", ...UNDECIDED },
};

// markIcon は丸の代わりに描く印の形 (style.css の .st.icon)。PR は open / draft / closed を形で分け、issue は 1 つ。
// 色は次の手のまま (toneOf) で、PR の状態は形だけが持つ。ほかのノードは丸で描くので undefined。
export function markIcon(n) {
  if (n.ref_kind === "issue") return "issue";
  if (n.ref_kind !== "pr") return undefined;
  if (n.state === "dropped") return "pr-closed";
  return n.pr_stages?.[0] === "draft" ? "pr-draft" : "pr-open";
}

// markClass は状態の印 (.st) の class。色の鍵、形の順に並べる。
export function markClass(n) {
  const icon = markIcon(n);
  return ["st", toneOf(n), ...(icon ? ["icon", icon] : [])].filter(Boolean).join(" ");
}

// leadStage は箱に出す段。段の無いノードと知らない語は undefined で、状態の語と色のまま描く。
export function leadStage(n) {
  return PR_STAGE[n.pr_stages?.[0]];
}

// toneOf は丸と chip の色の鍵。
export function toneOf(n) {
  const lead = leadStage(n);
  return lead ? lead.tone : STATE_TONE[n.state];
}

// cardText はカードの題と副題。grouped はセッションの枠の中のカードで、見出しが持つセッション名を繰り返さない。題が
// セッション名のまま (作業の記録に題が無い) のカードは、今の作業があればそれを題にする。label は状態の語。
export function cardText(n, label, grouped) {
  const summaryAsTitle = Boolean(grouped && n.title === n.session && n.summary);
  return {
    title: summaryAsTitle ? n.summary : n.title,
    sub: [!grouped && n.session !== n.title ? n.session : "", label, summaryAsTitle ? "" : n.summary].filter(Boolean).join(" · "),
  };
}

const ACCEPTANCE = /受け入れ条件\s*[:：]\s*/;
const BULLET = /^[-*・]\s*/;

// acceptanceLine はタスクの description からカードに添える 1 行を選ぶ。受け入れ条件の行があれば印から後ろ (印だけの行は
// 次の行を添える)、無ければ空でない 1 行目。書式を model に求めないので、印が無くても 1 行目が出る。
export function acceptanceLine(n) {
  if (n.kind !== "task" || !n.description) return "";
  const lines = n.description.split("\n").map((l) => l.trim()).filter(Boolean);
  const at = lines.findIndex((l) => ACCEPTANCE.test(l));
  if (at < 0) return lines[0] ?? "";
  const line = lines[at].slice(lines[at].search(ACCEPTANCE));
  if (line.replace(ACCEPTANCE, "") !== "" || at + 1 === lines.length) return line;
  return `${line} ${lines[at + 1].replace(BULLET, "")}`;
}

// cardHint はカードの tooltip。今の作業と、タスクの受け入れ条件を行に分ける。
export function cardHint(n) {
  return [n.summary, acceptanceLine(n)].filter(Boolean).join("\n");
}

// 人の手当てが要る状態。作業中より目立たせる。取りやめの参照は、待っていた下流を人が付け替えるまで止める。
const ATTENTION = new Set(["question", "permission", "limit", "error", "dropped"]);

// emphasis はカード全体に付ける強調の class。作業中は working、手当ての要る状態は attn と色の鍵、ほかは強調しない。
export function emphasis(n) {
  const lead = leadStage(n);
  if (lead) return lead.card;
  if (n.state === "working") return ["working"];
  if (ATTENTION.has(n.state)) return ["attn", STATE_TONE[n.state]];
  return [];
}
