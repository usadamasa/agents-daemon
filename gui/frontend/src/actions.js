// 操作。バイナリだけで済むものは結果の文面を、送達を伴うものは { ok, message } を返す。io はバイナリの呼び口で、
// テストでは差し替える。
import * as daemon from "./daemon.js";
import { destinationLabel, handText, handedText, instruction, parkText, spawnHandText, startText, statusText, unlinkText } from "./ops.js";

const defaultIO = {
  run: daemon.run,
  park: daemon.park,
};

// addBlocker は before が済まないと after を始められない辺を書く。端点はノードの key そのもの。
export async function addBlocker(before, after, io = defaultIO) {
  const ran = await io.run("edge add", { before: { ref: before.key, cwd: before.cwd ?? "" }, after: { ref: after.key, cwd: after.cwd ?? "" } });
  return "failure" in ran ? `依存を足せませんでした: ${ran.failure}` : `依存を足しました: ${before.title} → ${after.title}`;
}

// unlink は辺の元の記録を消す。session: の端点の辺は複数の辺になって描かれるので、まとめて消える。
export async function unlink(link, io = defaultIO) {
  const e = link.edge;
  if (e === undefined) return "";
  const ran = await io.run("edge rm", { before: { ref: e.before.ref, cwd: e.before.cwd }, after: { ref: e.after.ref, cwd: e.after.cwd } });
  return "failure" in ran ? `依存を外せませんでした: ${ran.failure}` : `依存を外しました: ${e.before.ref} → ${e.after.ref}`;
}

// adopt は終了したセッションのタスクをパーキングへ移す。ノードの key (task: の Ref) をそのまま渡す。
export async function adopt(node, io = defaultIO) {
  const failure = await io.park("park adopt", { ref: node.key });
  return failure === "" ? `タスク「${node.title}」をパーキングへ移しました` : `パーキングへ移せませんでした: ${failure}`;
}

// dismissOne はノード 1 つを一覧から外す。
export async function dismissOne(node, io = defaultIO) {
  const failure = await io.park("task dismiss", { ref: node.key });
  return failure === "" ? `タスク「${node.title}」を一覧から外しました` : `一覧から外せませんでした: ${failure}`;
}

// dismissAll は nodes を 1 つずつ外し、外した数と、外せなかった数と最初の理由を返す。1 つが外せなくても残りは続ける。
export async function dismissAll(nodes, io = defaultIO) {
  const failures = [];
  for (const n of nodes) {
    const failure = await io.park("task dismiss", { ref: n.key });
    if (failure !== "") failures.push(failure);
  }
  const failed = failures[0] === undefined ? "" : ` (${failures.length} つは外せませんでした: ${failures[0]})`;
  return `終了したセッションのタスク ${nodes.length - failures.length} つを一覧から外しました${failed}`;
}

// discard は open のメモを捨てる。ほかのセッションが先に取ったメモは捨てない (from open)。
export async function discard(memo, io = defaultIO) {
  const failure = await io.park("park set", { id: memo.id, status: "discarded", from: "open" });
  return failure === "" ? `メモ「${memo.title}」を捨てました` : `メモを捨てられませんでした: ${failure}`;
}

// reopenHint は handed や discarded のメモを人が open に戻すコマンドの形。
function reopenHint(id) {
  return `printf '%s' '{"id":"${id}","status":"open"}' | agents-daemon park set`;
}

// discardStale は最後に置かれてから days 日より経った open のメモをまとめて捨てる。捨てたメモは 7 日残り、
// id (`logs --events` の park.discard に残る) で戻せる。
export async function discardStale(days, io = defaultIO) {
  const ran = await io.run("park discard", { days, session_id: daemon.SESSION });
  if ("failure" in ran) return `古いメモを捨てられませんでした: ${ran.failure}`;
  let count;
  try {
    count = JSON.parse(ran.stdout).length;
  } catch (err) {
    return `古いメモを捨てましたが、結果を読めませんでした: ${String(err?.message ?? err)}`;
  }
  return `${days} 日より前のメモ ${count} つを捨てました (戻すには ${reopenHint("<id>")}、id は logs --events に残る)`;
}

// ---- 送達を伴う操作。結果は { ok, message } で、ok が false なら文面を残して理由を見せる ----

// tell はセッションの入力欄に text を打つ (`agents-daemon tell`)。送らなかった理由 (prompt が開いている、打ちかけの文がある、
// 上限の画面) はバイナリが返す。
async function tell(sessionID, text, io) {
  const ran = await io.run("tell", { to_session: sessionID, text, session_id: daemon.SESSION });
  if ("failure" in ran) return { ok: false, reason: ran.failure };
  try {
    const res = JSON.parse(ran.stdout);
    return { ok: true, warning: res.warning ?? "" };
  } catch (err) {
    return { ok: false, reason: `送った結果を読めませんでした: ${String(err?.message ?? err)}` };
  }
}

function sessionName(node) {
  return node.session ?? node.session_id ?? "";
}

// SAID は入力欄に打てたときの文面。セッションが読むのは次のターンなので、済んだとは言わない。
const SAID = " の入力欄に打ちました";

function told(name, sent, done = SAID) {
  if (sent.ok) return { ok: true, message: `${name}${done}${sent.warning ? ` (${sent.warning})` : ""}` };
  return { ok: false, message: `${name} へ送れませんでした: ${sent.reason}` };
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
  return told(sessionName(node), await tell(node.session_id, startText(node, note), io), " に着手を頼みました");
}

// setStatus は持ち主のセッションにタスクを completed か deleted にするよう頼む。
export async function setStatus(node, status, io = defaultIO) {
  return told(sessionName(node), await tell(node.session_id, statusText(node, status), io), ` に ${status} を頼みました`);
}

// handOffTask はタスクを to (生きているセッションか、新しく起こすセッションの repo) へ渡す。相手へ title と description を
// 送り、届いたら持ち主にタスクを消すよう頼む。起きたか分からない (window はできたが claude が前景にならなかった) ときは、
// 消すと誰も持たないタスクになりうるので頼まずに残す。
export async function handOffTask(node, to, note, io = defaultIO) {
  const name = destinationLabel(to);
  let toName = name;
  if (to.route === "spawn") {
    const ran = await io.run("spawn", { repo: to.repo, prompt: spawnHandText(node, to, note) });
    if ("failure" in ran) return { ok: false, message: `${name} で新しいセッションを起こせませんでした: ${ran.failure}` };
    let res;
    try {
      res = JSON.parse(ran.stdout);
    } catch (err) {
      return { ok: false, message: `起こした結果を読めませんでした: ${String(err?.message ?? err)}` };
    }
    if (!res.foreground) {
      return { ok: false, message: `${name} で window を作りましたが、claude が前景になりませんでした: ${res.detail ?? res.pane_id}。タスクは残してあります (起きていれば TaskUpdate で deleted にする)` };
    }
    toName = `${to.title} で起こした新しいセッション`;
  } else {
    const sent = await tell(to.session_id, handText(node, note), io);
    if (!sent.ok) return { ok: false, message: `${name} へ送れませんでした: ${sent.reason}` };
  }
  const owner = await tell(node.session_id, handedText(node, toName), io);
  const handed = to.route === "spawn" ? `${name} で新しいセッションを起こして渡し` : `${name} へ渡し`;
  if (!owner.ok) return { ok: true, message: `${handed}ましたが、${sessionName(node)} の入力欄に打てませんでした (タスクは残っています): ${owner.reason}` };
  return { ok: true, message: `${handed}、${sessionName(node)} にタスクを消すよう頼みました` };
}

// toParking はタスクをパーキングロットへ戻す。扱うメモ (捨てていないもの) があるタスクは新しいメモを作らず、handed の
// メモを open に戻してからタスクを消すよう頼む。頼めなくてもメモは一覧に戻っているので取り直さず、押し直しで依頼だけを
// 送り直せる (open のメモは戻す対象にならない)。
export async function toParking(node, io = defaultIO) {
  const kept = node.memos.filter((m) => m.status !== "discarded");
  const reopened = [];
  for (const memo of kept.filter((m) => m.status === "handed")) {
    const failure = await io.park("park set", { id: memo.id, status: "open", from: "handed" });
    if (failure !== "") return { ok: false, message: `メモ ${memo.id} を open に戻せませんでした: ${failure}${await reclaim(node, reopened, io)}` };
    reopened.push(memo.id);
  }
  const ids = kept.map((m) => m.id);
  const sent = await tell(node.session_id, parkText(node, ids), io);
  if (sent.ok) return { ok: true, message: ids.length === 0 ? `${sessionName(node)} にメモに置くよう頼みました` : `メモ ${ids.join(", ")} を open に戻し、${sessionName(node)} にタスクを消すよう頼みました` };
  if (ids.length === 0) return { ok: false, message: `${sessionName(node)} へ送れませんでした: ${sent.reason}` };
  return { ok: false, message: `メモ ${ids.join(", ")} は open に戻しましたが、${sessionName(node)} へタスクを消す依頼を送れませんでした: ${sent.reason}。もう一度「メモにする」を押すと依頼だけ送り直します` };
}

// reclaim は open に戻したメモを持ち主への handed に取り直し、結果の文面 (前の文に続ける) を返す。
async function reclaim(node, ids, io) {
  if (ids.length === 0) return "";
  const failed = [];
  for (const id of ids) {
    const failure = await io.park("park set", { id, status: "handed", from: "open", to_session: node.session_id });
    if (failure !== "") failed.push(`${id}: ${failure}`);
  }
  return failed.length === 0 ? "。メモは handed に戻しました" : `。メモを handed に戻せませんでした (${failed.join("; ")})`;
}

// handOff はメモを to (生きているセッションか、新しく起こすセッションの repo) へ渡す。取る・文面を組む・送る・届かなければ
// 戻す、はバイナリ (`park hand`) が 1 つの手順で行い、ここは結果を文面にする。intent は task (タスクにする) か
// issue (issue にする)。
export async function handOff(memo, to, intent, note, io = defaultIO) {
  const dest = to.route === "spawn" ? { to_repo: to.repo } : { to_session: to.session_id };
  const ran = await io.run("park hand", { id: memo.id, intent, note, ...dest, session_id: daemon.SESSION });
  if ("failure" in ran) return { ok: false, message: `メモを渡せませんでした: ${ran.failure}` };
  let res;
  try {
    res = JSON.parse(ran.stdout);
  } catch (err) {
    return { ok: false, message: `メモを渡した結果を読めませんでした: ${String(err?.message ?? err)}` };
  }
  const name = destinationLabel(to);
  const warned = res.warning ? ` (${res.warning})` : "";
  if (res.delivered) {
    return { ok: true, message: to.route === "spawn" ? `${name} で新しいセッションを起こし、メモを渡しました${warned}` : `${name} へ送り、メモを渡しました${warned}` };
  }
  if (res.status === "handed" && to.route === "spawn" && res.to) {
    // 起きたか分からないメモは戻していない。戻すと押し直しで 2 つ目のセッションが起きる。
    return { ok: false, message: `${name} で window を作りましたが、claude が前景になりませんでした: ${res.reason}。メモは handed のまま残してあります (起きていなければ ${reopenHint(memo.id)} で戻す)` };
  }
  const failed = to.route === "spawn" ? `${name} で新しいセッションを起こせませんでした` : `${name} へ送れませんでした`;
  const left = res.status === "open" ? "メモは open に戻しました" : `メモは handed のまま残っています (${reopenHint(memo.id)} で戻す)`;
  return { ok: false, message: `${failed}: ${res.reason}。${left}` };
}
