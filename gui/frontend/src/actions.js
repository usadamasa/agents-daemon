// 操作。バイナリだけで済むものは結果の文面を、送達を伴うものは { ok, message } を返す。io はバイナリの呼び口で、
// テストでは差し替える。結果の文面は footer に出るので英語で書き、バイナリが返す理由はそのまま添える。
import * as daemon from "./daemon.js";
import { destinationLabel, handText, handedText, instruction, parkText, spawnHandText, startText, statusText, unlinkText } from "./ops.js";

const defaultIO = {
  run: daemon.run,
  park: daemon.park,
};

// addBlocker は before が済まないと after を始められない辺を書く。端点はノードの key そのもの。
export async function addBlocker(before, after, io = defaultIO) {
  const ran = await io.run("edge add", { before: { ref: before.key, cwd: before.cwd ?? "" }, after: { ref: after.key, cwd: after.cwd ?? "" } });
  return "failure" in ran ? `Could not add dependency: ${ran.failure}` : `Added dependency: ${before.title} → ${after.title}`;
}

// unlink は辺の元の記録を消す。session: の端点の辺は複数の辺になって描かれるので、まとめて消える。
export async function unlink(link, io = defaultIO) {
  const e = link.edge;
  if (e === undefined) return "";
  const ran = await io.run("edge rm", { before: { ref: e.before.ref, cwd: e.before.cwd }, after: { ref: e.after.ref, cwd: e.after.cwd } });
  return "failure" in ran ? `Could not remove dependency: ${ran.failure}` : `Removed dependency: ${e.before.ref} → ${e.after.ref}`;
}

// noun はノードの呼び名。タスクの無いセッションのノードは work (#368)、辺の端点だけの置き場は session (#484)。
function noun(node) {
  if (node.kind === "ref") return "session";
  return node.kind === "session" ? "work" : "task";
}

// adopt は終了したセッションのタスクか作業をパーキングへ移す。ノードの key (task: か sid: の Ref) をそのまま渡す。
export async function adopt(node, io = defaultIO) {
  const failure = await io.park("park adopt", { ref: node.key });
  return failure === "" ? `Moved ${noun(node)} "${node.title}" to parking` : `Could not move to parking: ${failure}`;
}

// dismissOne はノード 1 つを一覧から外す。
export async function dismissOne(node, io = defaultIO) {
  const failure = await io.park("task dismiss", { ref: node.key });
  return failure === "" ? `Dismissed ${noun(node)} "${node.title}"` : `Could not dismiss: ${failure}`;
}

// dismissAll は nodes を 1 つずつ外し、外した数と、外せなかった数と最初の理由を返す。1 つが外せなくても残りは続ける。
export async function dismissAll(nodes, io = defaultIO) {
  const failures = [];
  for (const n of nodes) {
    const failure = await io.park("task dismiss", { ref: n.key });
    if (failure !== "") failures.push(failure);
  }
  const failed = failures[0] === undefined ? "" : ` (${failures.length} failed: ${failures[0]})`;
  return `Dismissed ${nodes.length - failures.length} tasks and work of ended sessions${failed}`;
}

// rename は作業の題を人の手で直す (`work set`、origin human)。以後の hook と companion はこの題を書き換えない。
export async function rename(node, subject, io = defaultIO) {
  const ran = await io.run("work set", { session_id: node.session_id, subject });
  return "failure" in ran ? `Could not rename: ${ran.failure}` : `Renamed to "${subject}"`;
}

// discard は open のメモを捨てる。ほかのセッションが先に取ったメモは捨てない (from open)。
export async function discard(memo, io = defaultIO) {
  const failure = await io.park("park set", { id: memo.id, status: "discarded", from: "open" });
  return failure === "" ? `Discarded memo "${memo.title}"` : `Could not discard the memo: ${failure}`;
}

// reopenHint は handed や discarded のメモを人が open に戻すコマンドの形。
function reopenHint(id) {
  return `printf '%s' '{"id":"${id}","status":"open"}' | agents-daemon park set`;
}

// discardStale は最後に置かれてから days 日より経った open のメモをまとめて捨てる。捨てたメモは 7 日残り、
// id (`logs --events` の park.discard に残る) で戻せる。
export async function discardStale(days, io = defaultIO) {
  const ran = await io.run("park discard", { days, session_id: daemon.SESSION });
  if ("failure" in ran) return `Could not discard old memos: ${ran.failure}`;
  let count;
  try {
    count = JSON.parse(ran.stdout).length;
  } catch (err) {
    return `Discarded old memos, but could not read the result: ${String(err?.message ?? err)}`;
  }
  return `Discarded ${count} memos older than ${days} days (restore with ${reopenHint("<id>")}; ids are in logs --events)`;
}

// ---- 送達を伴う操作。結果は { ok, message } で、ok が false なら文面を残して理由を見せる ----

// tell はセッションの入力欄に text を打つ (`agents-daemon tell`)。送らなかった理由 (prompt が開いている、打ちかけの文がある、
// セッションが上限で止まっている) はバイナリが返す。
async function tell(sessionID, text, io) {
  const ran = await io.run("tell", { to_session: sessionID, text, session_id: daemon.SESSION });
  if ("failure" in ran) return { ok: false, reason: ran.failure };
  try {
    JSON.parse(ran.stdout);
    return { ok: true };
  } catch (err) {
    return { ok: false, reason: `could not read the send result: ${String(err?.message ?? err)}` };
  }
}

function sessionName(node) {
  return node.session ?? node.session_id ?? "";
}

// SAID は入力欄に打てたときの文面。セッションが読むのは次のターンなので、済んだとは言わない。
const SAID = "typed into the input";

function told(name, sent, done = SAID) {
  if (sent.ok) return { ok: true, message: `${name}: ${done}` };
  return { ok: false, message: `Could not send to ${name}: ${sent.reason}` };
}

// instruct はノードのセッションへ指示を送る。
export async function instruct(node, text, io = defaultIO) {
  return told(sessionName(node), await tell(node.session_id, instruction(node, text), io));
}

// askUnlink は同じセッションの blockedBy の解消を after のセッションへ頼む。
export async function askUnlink(before, after, io = defaultIO) {
  return told(sessionName(after), await tell(after.session_id, unlinkText(before, after), io));
}

// start は持ち主のセッションに着手を頼む。
export async function start(node, note, io = defaultIO) {
  return told(sessionName(node), await tell(node.session_id, startText(node, note), io), "asked to start");
}

// setStatus は持ち主のセッションにタスクを completed か deleted にするよう頼む。
export async function setStatus(node, status, io = defaultIO) {
  return told(sessionName(node), await tell(node.session_id, statusText(node, status), io), `asked to set ${status}`);
}

// handOffTask はタスクを to (生きているセッションか、新しく起こすセッションの repo) へ渡す。相手へ title と description を
// 送り、届いたら持ち主にタスクを消すよう頼む。起きたか分からない (rex の session はできたが claude が前景にならなかった) ときは、
// 消すと誰も持たないタスクになりうるので頼まずに残す。
export async function handOffTask(node, to, note, io = defaultIO) {
  const name = destinationLabel(to);
  let toName = name;
  if (to.route === "spawn") {
    const ran = await io.run("spawn", { repo: to.repo, prompt: spawnHandText(node, to, note) });
    if ("failure" in ran) return { ok: false, message: `Could not start a new session in ${name}: ${ran.failure}` };
    let res;
    try {
      res = JSON.parse(ran.stdout);
    } catch (err) {
      return { ok: false, message: `Could not read the spawn result: ${String(err?.message ?? err)}` };
    }
    if (!res.foreground) {
      return { ok: false, message: `Created a new rex session in ${name}, but claude did not come to the foreground: ${res.detail ?? res.pane_id}. The task is kept (if it did start, set it to deleted with TaskUpdate)` };
    }
    // toName は持ち主のセッションへ送る文面に入るので日本語のまま。
    toName = `${to.title} で起こした新しいセッション`;
  } else {
    const sent = await tell(to.session_id, handText(node, note), io);
    if (!sent.ok) return { ok: false, message: `Could not send to ${name}: ${sent.reason}` };
  }
  const owner = await tell(node.session_id, handedText(node, toName), io);
  const handed = to.route === "spawn" ? `Started a new session in ${name} and handed off` : `Handed off to ${name}`;
  if (!owner.ok) return { ok: true, message: `${handed}, but could not type into ${sessionName(node)}'s input (the task remains): ${owner.reason}` };
  return { ok: true, message: `${handed}; asked ${sessionName(node)} to delete the task` };
}

// toParking はタスクをパーキングロットへ戻す。扱うメモ (捨てていないもの) があるタスクは新しいメモを作らず、handed の
// メモを open に戻してからタスクを消すよう頼む。頼めなくてもメモは一覧に戻っているので取り直さず、押し直しで依頼だけを
// 送り直せる (open のメモは戻す対象にならない)。
export async function toParking(node, io = defaultIO) {
  const kept = node.memos.filter((m) => m.status !== "discarded");
  const reopened = [];
  for (const memo of kept.filter((m) => m.status === "handed")) {
    const failure = await io.park("park set", { id: memo.id, status: "open", from: "handed" });
    if (failure !== "") return { ok: false, message: `Could not reopen memo ${memo.id}: ${failure}${await reclaim(node, reopened, io)}` };
    reopened.push(memo.id);
  }
  const ids = kept.map((m) => m.id);
  const sent = await tell(node.session_id, parkText(node, ids), io);
  if (sent.ok) return { ok: true, message: ids.length === 0 ? `Asked ${sessionName(node)} to park it as a memo` : `Reopened memo ${ids.join(", ")}; asked ${sessionName(node)} to delete the task` };
  if (ids.length === 0) return { ok: false, message: `Could not send to ${sessionName(node)}: ${sent.reason}` };
  return { ok: false, message: `Reopened memo ${ids.join(", ")}, but could not ask ${sessionName(node)} to delete the task: ${sent.reason}. Press Memo again to resend only the request` };
}

// reclaim は open に戻したメモを持ち主への handed に取り直し、結果の文面 (前の文に続ける) を返す。
async function reclaim(node, ids, io) {
  if (ids.length === 0) return "";
  const failed = [];
  for (const id of ids) {
    const failure = await io.park("park set", { id, status: "handed", from: "open", to_session: node.session_id });
    if (failure !== "") failed.push(`${id}: ${failure}`);
  }
  return failed.length === 0 ? ". The memos are handed again" : `. Could not set the memos back to handed (${failed.join("; ")})`;
}

// handOff はメモを to (生きているセッションか、新しく起こすセッションの repo) へ渡す。取る・文面を組む・送る・届かなければ
// 戻す、はバイナリ (`park hand`) が 1 つの手順で行い、ここは結果を文面にする。intent は task (タスクにする) か
// issue (issue にする)。
export async function handOff(memo, to, intent, note, io = defaultIO) {
  const dest = to.route === "spawn" ? { to_repo: to.repo } : { to_session: to.session_id };
  const ran = await io.run("park hand", { id: memo.id, intent, note, ...dest, session_id: daemon.SESSION });
  if ("failure" in ran) return { ok: false, message: `Could not hand off the memo: ${ran.failure}` };
  let res;
  try {
    res = JSON.parse(ran.stdout);
  } catch (err) {
    return { ok: false, message: `Could not read the hand-off result: ${String(err?.message ?? err)}` };
  }
  const name = destinationLabel(to);
  if (res.delivered) {
    return { ok: true, message: to.route === "spawn" ? `Started a new session in ${name} and handed off the memo` : `Sent to ${name} and handed off the memo` };
  }
  if (res.status === "handed" && to.route === "spawn" && res.to) {
    // 起きたか分からないメモは戻していない。戻すと押し直しで 2 つ目のセッションが起きる。
    return { ok: false, message: `Created a new rex session in ${name}, but claude did not come to the foreground: ${res.reason}. The memo stays handed (if it did not start, restore with ${reopenHint(memo.id)})` };
  }
  const failed = to.route === "spawn" ? `Could not start a new session in ${name}` : `Could not send to ${name}`;
  const left = res.status === "open" ? "The memo is open again" : `The memo stays handed (restore with ${reopenHint(memo.id)})`;
  return { ok: false, message: `${failed}: ${res.reason}. ${left}` };
}
