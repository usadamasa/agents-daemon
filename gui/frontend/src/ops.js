// 操作の判定、宛先の並べ方、タスクのセッションへ頼む文面の雛形。アプリはセッションではないので「自分 (self)」の
// 区別は無く、生きている全セッションのタスクに同じ動詞が出る。

// candidates は selected の「先に済ませるタスク」の候補: 辺の端点にできる (endpoint の) ノードのうち、自分、既にある上流、
// 下流 (足すと輪になる)、完了と終了したもの、セッションのノード (sid:) と同じセッションのタスクの組を除く。
export function candidates(g, selected) {
  const down = g.reach(selected.key);
  const already = new Set(g.blockers(selected.key).map((l) => l.before));
  return g.nodes.filter(
    (n) =>
      n.endpoint &&
      n.key !== selected.key &&
      !already.has(n.key) &&
      !down.has(n.key) &&
      !ownSession(n, selected) &&
      n.state !== "completed" &&
      n.state !== "exited",
  );
}

// ownSession は片方がセッションのノード (sid:) で、もう片方が同じセッションのものか。sid: はバイナリがそのセッションの
// タスクへ解くので、この組の辺はタスクどうしの待ちの輪になる (daemon の edge add も拒む)。
function ownSession(a, b) {
  const sid = a.key.startsWith("sid:") || b.key.startsWith("sid:");
  return sid && a.session_id !== undefined && a.session_id === b.session_id;
}

// canDismiss はノードを一覧から外せるか。終了したセッションのタスク (完了しているかを問わない)、作業 (タスクの無い
// セッション、#368)、辺の端点だけで残ったセッションの置き場 (#484)。TodoWrite の項目はセッションの 1 リストなので
// 1 項目では消せず、issue / PR の置き場は session_id を持たない。daemon が status.json を書いていない依存図は生きている
// セッションも live でないと描くので出さない。
export function canDismiss(node, data) {
  const dismissable = node.kind === "task" || node.kind === "session" || (node.kind === "ref" && node.state === "exited");
  return data.updated_at !== undefined && dismissable && !node.live && node.session_id !== undefined;
}

// bulkDismissable は一括で外す対象か。sid: を外すと端点の辺も消えるので、完了でも終了でもない下流が待っているカードは
// 単体の操作に残す (押した一覧に見えない待ちを、ひと押しで消さない)。
export function bulkDismissable(node, graph, data) {
  if (!canDismiss(node, data)) return false;
  if (node.kind === "task") return true;
  return graph.after(node.key).every((n) => n.state === "completed" || n.state === "exited");
}

// canAdopt はタスクか作業のノードをパーキングへ移せるか。外せるもののうち未完了のものだけで、記録の無い置き場は移せない。
// 押した後の判定はバイナリが status.json で改めて行う (`park adopt`)。
export function canAdopt(node, data) {
  return node.kind !== "ref" && canDismiss(node, data) && node.state !== "completed";
}

// canRename は作業の題を人が直せるか。作業の記録のあるセッションのノード (origin が pane でないもの) だけで、生きていても
// 終了していてもよい。直した題は origin human になり、hook と companion が書き換えない (`work set`)。
export function canRename(node) {
  return node.kind === "session" && node.origin !== undefined && node.origin !== "pane";
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

// ago は経過時間の語。1 時間に満たなければ「<1h ago」、1 日に満たなければ時間、それより前は日で数える。
function ago(ms) {
  if (ms < HOUR_MS) return "<1h ago";
  if (ms < DAY_MS) return `${Math.floor(ms / HOUR_MS)}h ago`;
  return `${Math.floor(ms / DAY_MS)}d ago`;
}

// lastSeenAgo は起こす repo の候補を最後に見た時刻から now (ミリ秒) までの経過の語。時刻が無ければ空。
export function lastSeenAgo(to, now) {
  const at = to.last_seen === undefined ? NaN : Date.parse(to.last_seen);
  return Number.isNaN(at) ? "" : ago(now - at);
}

// spawnLabel は起こす repo の候補の名前。最後に見た時刻があれば経過を添える。
export function spawnLabel(to, now) {
  const seen = lastSeenAgo(to, now);
  return seen === "" ? to.title : `${to.title} (${seen})`;
}

// pathDestination は入力されたパスを起こす先にする。絶対パスでなければ理由を返す (バイナリも同じ検査をするが、
// 起こす前に手元で弾いて知らせる)。
export function pathDestination(path) {
  const repo = path.trim();
  if (!repo.startsWith("/")) return { failure: `repo must be an absolute path: ${repo}` };
  return { route: "spawn", repo, title: basename(repo) };
}

function basename(path) {
  return path.replace(/\/+$/, "").split("/").pop() ?? path;
}

// withNote は利用者が補足欄に書いた文を、操作の文面の末尾に足す。空なら text のまま。メモを渡す文面の雛形は
// バイナリ (parkhand) が持ち、アプリが組むのはタスクへの依頼だけ。
export function withNote(text, note) {
  const said = note.trim();
  return said === "" ? text : `${text}\n利用者からの補足: ${said}`;
}

// canTell はノードのセッションへ文面を打てるか (生きていて、セッションを持つ)。pane の状態 (prompt、上限、打ちかけの文) は
// 送るときにバイナリが見る。
export function canTell(node) {
  return node.live && typeof node.session_id === "string" && node.session_id !== "";
}

// isOwnTask はセッションが動かせる (未完了の) タスクか。着手・完了・削除・メモにする、の対象。
export function isOwnTask(node) {
  return node.kind === "task" && canTell(node) && node.state !== "completed";
}

// uniqueLive は sessions を受け取った順に宛先にする。exclude と、同じセッションの 2 回目は除く (subject は最初のもの)。
function uniqueLive(sessions, exclude) {
  const seen = new Set();
  const out = [];
  for (const s of sessions) {
    if (!s.live || s.session_id === "" || s.session_id === exclude || seen.has(s.session_id)) continue;
    seen.add(s.session_id);
    out.push({ route: "peer", session_id: s.session_id, session: s.session, subject: s.subject });
  }
  return out;
}

// taskDestinations は「タスクにする」の宛先: メモの repo の生きているセッション。
export function taskDestinations(repo) {
  return uniqueLive(repo.sessions);
}

// issueDestinations は「issue にする」の宛先: メモの repo のセッションを先に、続けてほかの repo の生きているセッション。
export function issueDestinations(data, repo) {
  return uniqueLive([...repo.sessions, ...data.repos.flatMap((r) => r.sessions)]);
}

// peerDestinations はタスクを渡す宛先: repo を問わず生きているほかのセッション (持ち主を除く)。
export function peerDestinations(data, owner) {
  return uniqueLive(data.repos.flatMap((r) => r.sessions), owner);
}

// SUBJECT_CHARS は宛先の名前に添える subject の長さの上限。
const SUBJECT_CHARS = 28;

// destinationLabel は宛先の名前。セッションには何をしているか (subject) を添える。
export function destinationLabel(to) {
  if (to.route === "spawn") return to.title;
  if (to.subject === "") return to.session;
  const subject = to.subject.length > SUBJECT_CHARS ? `${to.subject.slice(0, SUBJECT_CHARS - 1)}…` : to.subject;
  return `${to.session} (${subject})`;
}

// ---- タスクのセッションへ頼む文面 ----

function ref(node) {
  return `タスク #${node.task_id ?? ""}「${node.title}」`;
}

// instruction は指示の文面。タスクのノードなら、どのタスクについての指示かを title で添える。
export function instruction(node, text) {
  return node.kind === "task" ? `タスク「${node.title}」について: ${text}` : text;
}

// unlinkText は同じセッションの blockedBy の解消を頼む文面。TaskUpdate に blockedBy を外す引数が無いので、
// 待たれている側を片付けるか、待っている側を作り直すよう頼む。
export function unlinkText(before, after) {
  const id = (n) => n.task_id ?? "";
  return (
    `タスク #${id(after)}「${after.title}」が #${id(before)}「${before.title}」の完了待ち (blockedBy) になっています。` +
    `この待ちが要らなければ、#${id(before)} を completed か deleted にするか、#${id(after)} を blockedBy なしで作り直してください。`
  );
}

export function startText(node, note = "") {
  return withNote(`${ref(node)}に着手してください。TaskUpdate で in_progress にしてから進めてください。`, note);
}

export function statusText(node, status) {
  return `${ref(node)}を TaskUpdate で ${status} にしてください。`;
}

// handText は渡す相手へ送る文面 (title と description)。補足 (note) は末尾に足す。
export function handText(node, note = "") {
  const lines = [`${node.session ?? ""} (session ID ${node.session_id ?? ""}) からタスク「${node.title}」を渡します。TaskCreate で作って進めてください。`];
  if (node.description) lines.push(`詳細: ${node.description}`);
  return withNote(lines.join("\n"), note);
}

// spawnHandText は新しく起こすセッションの最初のターンの文面。起こした経緯を 1 行置き、続けて渡す文面。
export function spawnHandText(node, to, note = "") {
  return `このタスクを渡すため、${to.title} (${to.repo}) で新しいセッションとして起こしました。\n${handText(node, note)}`;
}

// handedText は渡した後に持ち主へタスクを消すよう頼む文面。
export function handedText(node, toName) {
  return `${ref(node)}を ${toName} へ渡しました (title と description を送った)。TaskUpdate で deleted にしてください。`;
}

// parkText はタスクをパーキングロットへ戻すよう頼む文面。扱うメモ (捨てていないもの) があればそれを open に戻した後に
// タスクを消すよう頼み、無ければ park tool でメモに置いてから消すよう頼む。
export function parkText(node, reopenedIDs) {
  if (reopenedIDs.length === 0) {
    return `${ref(node)}をパーキングロットへ戻してください。park tool でタスクの title と description をメモ (kind は scope) に置き、置けたら TaskUpdate で deleted にしてください。`;
  }
  return `${ref(node)}の元のメモ (${reopenedIDs.join(", ")}) をパーキングロットの open に戻しました。TaskUpdate で deleted にしてください。`;
}
