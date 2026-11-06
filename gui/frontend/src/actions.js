// バイナリだけで済む操作。hooks/deps.tsx、hooks/dismiss.ts、hooks/parking.ts の操作を移した。どれも結果の文面を返す。
// io はバイナリの呼び口で、テストでは差し替える。
import * as daemon from "./daemon.js";
import { spawnText } from "./ops.js";

const defaultIO = {
  run: daemon.run,
  park: daemon.park,
  logEvent: daemon.logEvent,
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

// spawnSession は repo で新しいセッションを起こし、本文を最初のターンにする。exit 0 で foreground が false なら
// window はできていて claude が前景にならなかった (unknown)。
async function spawnSession(repo, text, io) {
  const ran = await io.run("spawn", { repo, prompt: text });
  if ("failure" in ran) return { to: "", failure: ran.failure };
  try {
    const res = JSON.parse(ran.stdout);
    return res.foreground ? { to: res.session_id } : { to: res.session_id, unknown: res.detail ?? `pane ${res.pane_id} で claude が前景にならなかった` };
  } catch (err) {
    return { to: "", failure: String(err?.message ?? err) };
  }
}

// handOffToNew はメモを新しく起こすセッションへ渡す。送る前にメモを取り (open のときだけ handed にし、宛先を同じ park set で
// 書く)、起こせなければ open に戻す。取れなかった (ほかのセッションが先に渡した、捨てた) メモは起こさない。
// 手順の交錯で破れないことは models/handoff の Handoff.tla (Claim → Spawn → Revert) が持つ。
export async function handOffToNew(memo, to, intent, note, io = defaultIO) {
  const text = spawnText(memo, to, intent, note);
  const sent = { via: "memo", memo: memo.id, repo: to.repo };
  const claim = await io.park("park set", { id: memo.id, status: "handed", from: "open", to_repo: to.repo });
  if (claim !== "") {
    await io.logEvent("mod.send", { ...sent, to: "", delivered: false, reason: `claim: ${claim}` });
    return `メモを取れませんでした (ほかのセッションが先に渡したか、捨てたかもしれません): ${claim}`;
  }
  const delivered = await spawnSession(to.repo, text, io);
  if ("unknown" in delivered) {
    // 起きたか分からないメモは戻さない。戻すと押し直しで 2 つ目のセッションが起きる。
    await io.logEvent("mod.send", { ...sent, to: delivered.to, delivered: false, reason: delivered.unknown });
    return `${to.title} で window を作りましたが、claude が前景になりませんでした: ${delivered.unknown}。メモは handed のまま残してあります (起きていなければ ${reopenHint(memo.id)} で戻す)`;
  }
  if ("failure" in delivered) {
    await io.logEvent("mod.send", { ...sent, to: delivered.to, delivered: false, reason: delivered.failure });
    const revert = await io.park("park set", { id: memo.id, status: "open", from: "handed" });
    const reverted = revert === "" ? "メモは open に戻しました" : `メモを open に戻せませんでした (${reopenHint(memo.id)} で戻す): ${revert}`;
    return `${to.title} で新しいセッションを起こせませんでした: ${delivered.failure}。${reverted}`;
  }
  await io.logEvent("mod.send", { ...sent, to: delivered.to, delivered: true });
  return `${to.title} で新しいセッションを起こし、メモを渡しました`;
}
