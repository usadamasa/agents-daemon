// 操作の判定と文面。hooks/graph.ts の candidates、hooks/dismiss.ts の canDismiss、hooks/parking.ts の宛先と文面のうち
// engine に依らない部分を移した。アプリはセッションではないので「自分 (self)」の区別は無い。
import { kindLabel } from "./memo.js";

// candidates は selected の「先に済ませるタスク」の候補: 辺の端点にできる (endpoint の) ノードのうち、自分、既にある上流、
// 下流 (足すと輪になる)、完了と終了したものを除く。
export function candidates(g, selected) {
  const down = g.reach(selected.key);
  const already = new Set(g.blockers(selected.key).map((l) => l.before));
  return g.nodes.filter(
    (n) => n.endpoint && n.key !== selected.key && !already.has(n.key) && !down.has(n.key) && n.state !== "completed" && n.state !== "exited",
  );
}

// canDismiss はノードを一覧から外せるか。終了したセッションのタスク (完了しているかを問わない) だけ。TodoWrite の
// 項目はセッションの 1 リストなので 1 項目では消せず、置き場のノードには消す記録が無い。daemon が status.json を
// 書いていない依存図は生きているセッションも live でないと描くので出さない。
export function canDismiss(node, data) {
  return data.updated_at !== undefined && node.kind === "task" && !node.live && node.session_id !== undefined;
}

// canAdopt はタスクのノードをパーキングへ移せるか。外せるタスクのうち未完了のものだけ。押した後の判定はバイナリが
// status.json で改めて行う (`park adopt`)。
export function canAdopt(node, data) {
  return canDismiss(node, data) && node.state !== "completed";
}

// spawnDestinations は「新しいセッションを起こして渡す」の repo の候補: first (メモの repo)、依存図に出ている repo、
// セッション台帳で最近動いた repo (新しい順) の順 (重複は除く)。台帳に載った repo には最後に見た時刻を添える。
export function spawnDestinations(data, first) {
  const lastSeen = new Map(data.recent_repos.map((r) => [r.repo, r.last_seen]));
  const seen = new Set();
  const out = [];
  const add = (repo, title) => {
    if (repo === "" || seen.has(repo)) return;
    seen.add(repo);
    const at = lastSeen.get(repo);
    out.push(at === undefined ? { route: "spawn", repo, title } : { route: "spawn", repo, title, last_seen: at });
  };
  if (first !== undefined) add(first.repo, first.title);
  for (const r of data.repos) add(r.repo, r.title);
  for (const r of data.recent_repos) add(r.repo, r.title);
  return out;
}

const HOUR_MS = 60 * 60 * 1000;
const DAY_MS = 24 * HOUR_MS;

// ago は経過時間の語。1 時間に満たなければ「1 時間以内」、1 日に満たなければ時間、それより前は日で数える。
function ago(ms) {
  if (ms < HOUR_MS) return "1 時間以内";
  if (ms < DAY_MS) return `${Math.floor(ms / HOUR_MS)} 時間前`;
  return `${Math.floor(ms / DAY_MS)} 日前`;
}

// spawnLabel は起こす repo の候補の名前。最後に見た時刻があれば now (ミリ秒) からの経過を添える。
export function spawnLabel(to, now) {
  const at = to.last_seen === undefined ? NaN : Date.parse(to.last_seen);
  return Number.isNaN(at) ? to.title : `${to.title} (${ago(now - at)})`;
}

// pathDestination は入力されたパスを起こす先にする。絶対パスでなければ理由を返す (バイナリも同じ検査をするが、
// 起こす前に手元で弾いて知らせる)。
export function pathDestination(path) {
  const repo = path.trim();
  if (!repo.startsWith("/")) return { failure: `repo は絶対パスで指定してください: ${repo}` };
  return { route: "spawn", repo, title: basename(repo) };
}

function basename(path) {
  return path.replace(/\/+$/, "").split("/").pop() ?? path;
}

// withNote は利用者が補足欄に書いた文を、操作の文面の末尾に足す。空なら text のまま。
export function withNote(text, note) {
  const said = note.trim();
  return said === "" ? text : `${text}\n利用者からの補足: ${said}`;
}

// PREMISE_CHECK は渡す文面で依頼の前に置く確かめ。メモは置いた時点の前提で書かれていて、渡すまでにレビューや別の
// 変更で前提が消えていることがある。渡したメモは handed になり一覧から消えるので、受け手はメモの status を触らずに終えてよい。
const PREMISE_CHECK = "先に、メモの前提 (詳細と関係するファイル) が今のコードと計画で成り立つかを確かめてください。";
const PREMISE_GONE = "前提が成り立たないとき (該当箇所が無くなった、別の変更で直った) は、その理由を 1 行で返して終えてください。";

// taskText は「タスクにする」で送る文面。着手の時期は受け手の計画に任せる。依存図はタスクの description に
// 書かれた id でメモと照合する (deps/graph)。
export function taskText(memo, note = "") {
  const text = [
    `パーキングロットのメモ「${memo.title}」(種類: ${kindLabel(memo)}) をタスクにしてください。` +
      PREMISE_CHECK +
      `成り立つときは TaskCreate で作り、タスクの description にメモの id (${memo.id}) を入れてください。` +
      `既にこのメモを扱うタスクがあれば、新しく作らずにその description に id を書き足すだけで構いません。` +
      `着手の時期は今の計画 (タスクの依存) に従ってください。` +
      PREMISE_GONE,
    ...context(memo),
  ].join("\n");
  return withNote(text, note);
}

// issueText は「issue にするよう頼む」で送る文面。
export function issueText(memo, repo, note = "") {
  const text = [
    `パーキングロットのメモ「${memo.title}」を GitHub の issue にしてください。repo: ${repo}。` + PREMISE_CHECK + "成り立つときは起票し、URL を返してください。" + PREMISE_GONE,
    ...context(memo),
  ].join("\n");
  return withNote(text, note);
}

// spawnText は新しく起こすセッションの最初のターンの文面。起こした経緯を 1 行置き、続けて渡す文面。
// issue はメモの repo ではなく起こした repo を起票先にする (生きているセッションの無い repo へ渡すための経路なので)。
export function spawnText(memo, to, intent, note = "") {
  const body = intent === "task" ? taskText(memo, note) : issueText(memo, to.title, note);
  return `このメモを渡すため、${to.title} (${to.repo}) で新しいセッションとして起こしました。\n${body}`;
}

function context(memo) {
  const lines = [];
  if (memo.detail) lines.push(`詳細: ${memo.detail}`);
  for (const source of memo.sources) {
    if (source.detail) lines.push(`補足 (${source.session}): ${source.detail}`);
  }
  if (memo.files?.length) lines.push(`関係するファイル: ${memo.files.join(", ")}`);
  if (memo.default_taken) lines.push(`置いたときの既定: ${memo.default_taken}`);
  lines.push(`(メモ ${memo.id})`);
  return lines;
}
