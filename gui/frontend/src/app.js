// 依存図の描画。`agents-daemon deps` の JSON (protocol.schema.json の DepsData) を Go 側の Daemon.Deps で読み、
// dagre で配置して箱 (HTML) と線 (SVG) を描く。段と下流の数と原因の鎖はバイナリが数えて渡すので、ここは並べるだけ。
// オブジェクトはタスク (図の箱) とメモ (下の一覧の行)。選ぶとそのそばにポップアップで単体ビューが開く。
import * as dagre from "./vendor/dagre.esm.js";
import { Graph } from "./graph.js";
import { GROUP_W, fitLimit, groupBySession, partition, placeGroups } from "./column.js";
import { fitEdges, spreadPorts } from "./ports.js";
import { findJumps, jumpPath } from "./jumps.js";
import { PR_STAGE, STATE_LABEL, cardHint, cardText, emphasis, leadStage, markClass, toneOf } from "./card.js";
import { isStale, kindLabel, lastPlacedAt, stamp } from "./memo.js";
import { bulkDismissable, canAdopt, canDismiss, canRename, canTell, candidates, isOwnTask, issueDestinations, lastSeenAgo, pathDestination, peerDestinations, spawnDestinations, taskDestinations } from "./ops.js";
import { addBlocker, adopt, askUnlink, discard, discardStale, dismissAll, dismissOne, handOff, handOffTask, instruct, rename, setStatus, start, toParking, unlink } from "./actions.js";
import { openTarget, pickFolder } from "./daemon.js";
import { segments } from "./links.js";
import { CONTROLS_BAND, MAX_ZOOM, MIN_ZOOM, centerOffset, clampZoom, fitZoom, overflowCenter, percent, scaledFrame, scrollAfterZoom, stepZoom, topOffset, wheelZoomer } from "./zoom.js";
import { restore, scrollView, watchUpgrade } from "./restore.js";

const NODE_W = 240;
const NODE_H = 72;
// 単体ビューの幅。style.css の .pop と同じ数 (Primer の overlay の small)。
const POP_W = 320;
// 窓が前にあるときは数秒おき、隠れているときは間隔を伸ばす。
const POLL_MS = 3000;
const POLL_HIDDEN_MS = 15000;

const REF_KIND_LABEL = { issue: "issue", pr: "PR" };
const KIND_LABEL = { task: "Task", todo: "TodoWrite item", session: "Session work", ref: "Ref" };
// companion が会話から付けた題 (origin inferred) に添える chip の文 (出どころの説明)。直すのは題そのものを押す (OOUI:
// 対象を選んでから動詞)。最初の prompt の題、人が直した題、記録の無い pane の名前には何も添えない。
const INFERRED_CHIP = "Auto-titled";

const state = {
  data: null,
  graph: null,
  error: "",
  showCompleted: false,
  readAt: null,
  repoFilter: "",
  // lastText は最後に描いた deps の JSON の文字列。同じなら描き直さない。
  lastText: "",
  // selected は選んだオブジェクト。{ type: "node", key } か { type: "memo", id }。
  selected: null,
  // picker はポップアップの中で開いている選択。"blocker" (先に済ませるタスク)、"instruct" (指示の文面)、"hand" (タスクを渡す先)、
  // "start" (着手の補足)、"task" / "issue" (メモを渡す先)。
  picker: null,
  // note は補足欄、draft は指示の文面の下書き。組み直しても消えないようにここに持つ。
  note: "",
  draft: "",
  // refusal は送達が拒まれた理由。文面の下に出し、文面は残す。
  refusal: "",
  // moreRepos は起こす repo の候補を全部出しているか。
  moreRepos: false,
  // notice は最後の操作の結果。footer に出し、noticeUntil (ミリ秒) まで残す。
  notice: "",
  noticeUntil: 0,
  // busy は操作を走らせている最中か。二重に押さないようにする。
  busy: false,
};

const NOTICE_MS = 10000;

// act は操作を走らせ、結果を footer に出して依存図を読み直す。結果が { ok: false } なら (送達が拒まれた) 開いている選択と
// 打った文面を残し、理由を文面の下に出す。
async function act(run) {
  if (state.busy) return;
  state.busy = true;
  state.refusal = "";
  renderPopup();
  let result;
  try {
    result = await run();
  } finally {
    state.busy = false;
  }
  const ok = typeof result === "string" || result.ok;
  state.notice = typeof result === "string" ? result : result.message;
  state.noticeUntil = Date.now() + NOTICE_MS;
  if (ok) {
    state.picker = null;
    state.note = "";
    state.draft = "";
  } else {
    state.refusal = result.message;
  }
  state.lastText = "";
  await fetchDeps();
  setTimeout(renderFoot, NOTICE_MS + 100);
}

const el = (id) => document.getElementById(id);

// openLink は本文のリンクを開く。単体ビューの選択と打ちかけの文面に触らないよう act を通さず、失敗だけを footer に出す。
async function openLink(target) {
  const failure = await openTarget(target);
  if (failure === "") return;
  state.notice = failure;
  state.noticeUntil = Date.now() + NOTICE_MS;
  renderFoot();
  setTimeout(renderFoot, NOTICE_MS + 100);
}

// linkContext は repo の鍵 (owner/repo を引く) と base (相対パスの基準) を segments の ctx にする。
function linkContext(repo, base) {
  return { githubRepo: state.data.repos.find((r) => r.repo === repo)?.github_repo, base };
}

// appendLinked は text の URL、issue / PR の参照、ファイルパスをリンクにして parent に足す。
function appendLinked(parent, text, ctx) {
  for (const s of segments(text, ctx)) {
    if (s.target === undefined) {
      parent.append(s.text);
      continue;
    }
    const a = document.createElement("a");
    a.className = "ref";
    a.href = "#";
    a.title = s.target;
    a.textContent = s.text;
    a.addEventListener("click", (e) => {
      e.preventDefault();
      void openLink(s.target);
    });
    parent.append(a);
  }
}

function stateLabel(n) {
  // 下流が全部完了した idle の peer は報告を済ませていて、こちらの返事を要さない。
  const label = leadStage(n)?.label ?? (n.state === "done" && state.graph.settled(n.key) ? "Idle" : (STATE_LABEL[n.state] ?? n.state));
  // 題を引いた issue / PR は題が番号を含まないので、番号と repo を状態に添える。
  if (n.ref_label && n.ref_label !== n.title) return `${label} · ${n.ref_label}`;
  if (n.ref_kind) return `${label} · ${REF_KIND_LABEL[n.ref_kind] ?? n.ref_kind}`;
  return n.detail ? `${label}、${n.detail}` : label;
}

// ---- 読み込み ----

async function fetchDeps() {
  let changed = true;
  try {
    const text = await window.wails.Call.ByName("main.Daemon.Deps");
    // 中身が同じなら DOM を組み直さない (組み直すとスクロールの位置とホバーが戻り、押している最中の要素が消える)。
    changed = text !== state.lastText;
    if (changed) {
      const data = JSON.parse(text);
      if (!Array.isArray(data.graph?.nodes)) throw new Error("deps JSON has no graph");
      state.data = data;
      state.graph = new Graph(data.graph);
      state.lastText = text;
    }
    state.error = "";
    state.readAt = new Date();
  } catch (err) {
    // バイナリが無い、daemon の state が読めない、のどちらもここ。前の依存図は残す。
    state.error = String(err?.message ?? err);
  }
  if (changed || state.data === null) render();
  else renderFoot();
}

function schedule() {
  const wait = document.hidden ? POLL_HIDDEN_MS : POLL_MS;
  setTimeout(async () => {
    // 描画が例外を投げても読み直しを張り直す。張り直さないと、図がそれきり更新されない。
    try {
      await fetchDeps();
    } finally {
      schedule();
    }
  }, wait);
}

// ---- 選択 ----

function select(next) {
  state.selected = next;
  state.picker = null;
  state.note = "";
  state.moreRepos = false;
  render();
}

function selectedNode() {
  return state.selected?.type === "node" ? state.graph.node(state.selected.key) : undefined;
}

// memos は repo の絞り込みを掛けた open のメモ (最後に置かれた順の新しい順)。repo の title も添える。
function memos() {
  const out = [];
  for (const r of state.data.repos) {
    if (state.repoFilter !== "" && r.repo !== state.repoFilter) continue;
    for (const m of r.memos) out.push({ memo: m, repo: r });
  }
  return out.sort((a, b) => lastPlacedAt(b.memo).localeCompare(lastPlacedAt(a.memo)));
}

function selectedMemo() {
  if (state.selected?.type !== "memo") return undefined;
  for (const r of state.data.repos) {
    const m = r.memos.find((x) => x.id === state.selected.id);
    if (m) return { memo: m, repo: r };
  }
  return undefined;
}

// ---- 図 ----

function visibleNodes() {
  return state.graph.view(state.showCompleted).nodes;
}

// chainEdges は longest_chain の隣り合う 2 つを before→after の辺として持つ。
function chainEdges(graph) {
  const set = new Set();
  const c = graph.longest_chain ?? [];
  for (let i = 0; i + 1 < c.length; i++) set.add(`${c[i]}\u0000${c[i + 1]}`);
  return set;
}

// rankByDepth はバイナリの段 (前提をたどった最長の深さ、根が 0。完了を隠す図では folded で数え直したもの) を
// そのまま段にする ranker。dagre の組み込みの
// ranker はどれも前提の無いノードを下流の直前へ寄せる (A→B→C と D→C で D が B の段に来る) ので使わない。
// layout は辺のラベルの場所を空けるために辺の minlen を 2 倍にし、組み込みの ranker が出す rank は偶数になる。
// depth も 2 倍にして同じ尺に揃える (@dagrejs/dagre 3.1.3 の内部の前提。奇数の rank を渡すと layout が落ちる)。
function rankByDepth(depth) {
  return (g) => {
    for (const v of g.nodes()) g.node(v).rank = (depth.get(v) ?? 0) * 2;
  };
}

const MARGIN_X = 64;
const MARGIN_Y = 24;
const GAP_X = 48;
const GAP_Y = 44;

// layout は辺でつながるノード (connected) を dagre で配置する。辺の無いノードは図の下のセッションの枠 (renderGroups) か
// 右の列 (renderColumn) に並べる。depth は key → 段 (Graph.view の depth)。返すのは key → { x, y } (中心) と、全体の width、height、辺の経路を持つ
// dagre の graph。
// 待ちの輪の辺は段が下へ進まない (輪の両端をバイナリが同じ段か逆の段に数える)。rankByDepth の段で dagre に渡すと
// layout が落ちるので、段が下へ進む辺だけを dagre に渡し、残り (back) は箱の中心どうしを結ぶ点列で返す。
function layout(connected, links, depth) {
  const g = new dagre.graphlib.Graph();
  g.setGraph({ rankdir: "TB", ranker: rankByDepth(depth), nodesep: GAP_X, ranksep: GAP_Y, marginx: MARGIN_X, marginy: MARGIN_Y });
  g.setDefaultEdgeLabel(() => ({}));
  for (const n of connected) g.setNode(n.key, { width: NODE_W, height: NODE_H });
  const rest = [];
  for (const l of links) {
    if (!g.hasNode(l.before) || !g.hasNode(l.after)) continue;
    if ((depth.get(l.after) ?? 0) > (depth.get(l.before) ?? 0)) g.setEdge(l.before, l.after);
    else rest.push(l);
  }
  dagre.layout(g);
  // renderGraph は connected が空なら layout を呼ばないので、ここでは空を見ない。
  fitEdges(g, MARGIN_X, MARGIN_Y);
  const pos = new Map(connected.map((n) => [n.key, g.node(n.key)]));
  const back = rest.map((l) => {
    const a = pos.get(l.before);
    const b = pos.get(l.after);
    return { ...l, points: [{ x: a.x, y: a.y }, { x: (a.x + b.x) / 2, y: (a.y + b.y) / 2 }, { x: b.x, y: b.y }] };
  });
  return { g, pos, back, width: g.graph().width, height: g.graph().height };
}

function nodeCard(n, highlight, grouped = false) {
  const b = document.createElement("button");
  b.type = "button";
  b.className = "node";
  b.classList.add(...emphasis(n));
  if (n.halted) b.classList.add("halt");
  if (n.state === "exited" || n.state === "completed") b.classList.add("gone");
  if (state.selected?.type === "node" && state.selected.key === n.key) b.classList.add("sel");
  if (highlight !== null && !highlight.has(n.key)) b.classList.add("dim");
  b.dataset.key = n.key;
  b.addEventListener("click", (e) => {
    e.stopPropagation();
    select(state.selected?.type === "node" && state.selected.key === n.key ? null : { type: "node", key: n.key });
  });

  const dot = document.createElement("span");
  dot.className = markClass(n);

  const lb = document.createElement("span");
  lb.className = "lb";
  const text = cardText(n, stateLabel(n), grouped);
  const title = document.createElement("b");
  title.textContent = text.title;
  const sub = document.createElement("small");
  sub.textContent = text.sub;
  const hint = cardHint(n);
  if (hint) b.title = hint;
  lb.append(title, sub);
  b.append(dot, lb);

  const badges = [];
  if (n.stuck && n.downstream > 0) badges.push(`↓${n.downstream}`);
  if (n.parked > 0) badges.push(`◇${n.parked}`);
  if (n.deadlock) badges.push("⟳");
  if (badges.length > 0) {
    const bd = document.createElement("span");
    bd.className = "bd";
    bd.textContent = badges.join(" ");
    b.append(bd);
  }
  return b;
}

const SVG = "http://www.w3.org/2000/svg";
// 交点の飛び越しの弧の半径と、線の端 (ノードの縁、矢じり) から飛び越しを置かない距離。拡大縮小は canvas ごと
// 変わるので、弧も同じ率で伸び縮みする。
const JUMP_R = 8;
const JUMP_MARGIN = 20;

function edgePath(d, cls, marker, hint) {
  const p = document.createElementNS(SVG, "path");
  p.setAttribute("d", d);
  if (cls) p.setAttribute("class", cls);
  p.setAttribute("marker-end", `url(#${marker})`);
  if (!hint) return p;
  // 線は細くてホバーしにくいので、太い透明な線を重ねて当たりにする。
  const group = document.createElementNS(SVG, "g");
  group.setAttribute("class", "hint");
  const hit = document.createElementNS(SVG, "path");
  hit.setAttribute("d", d);
  hit.setAttribute("class", "hit");
  const t = document.createElementNS(SVG, "title");
  t.textContent = hint;
  group.append(t, p, hit);
  return group;
}

// foldedHint は畳んだ辺のホバーの文。通り抜けた完了ノードの題を並べる。
function foldedHint(l) {
  return `via ${l.via.map((key) => state.graph.node(key)?.title ?? key).join(" → ")}`;
}

function arrowMarker(id, cls) {
  const m = document.createElementNS(SVG, "marker");
  m.setAttribute("id", id);
  m.setAttribute("orient", "auto");
  m.setAttribute("markerWidth", "5");
  m.setAttribute("markerHeight", "5");
  m.setAttribute("refX", "4");
  m.setAttribute("refY", "2");
  if (cls) m.setAttribute("class", cls);
  const path = document.createElementNS(SVG, "path");
  path.setAttribute("d", "M0 0 L4 2 L0 4 Z");
  m.append(path);
  return m;
}

// highlightKeys はメモを選んでいるとき明るく出すノード (メモを置いたときに進めていたタスク)。選んでいなければ null。
function highlightKeys() {
  const sel = selectedMemo();
  if (sel === undefined) return null;
  return new Set(sel.memo.sources.map((s) => s.task_ref).filter(Boolean));
}

function renderGraph() {
  const host = el("graph");
  const graph = state.data.graph;
  const { nodes, links, depth } = state.graph.view(state.showCompleted);
  const { connected, free, ended } = partition(nodes, links, graph.column_order);
  const highlight = highlightKeys();
  renderColumn(ended, highlight);
  const scroll = { left: host.scrollLeft, top: host.scrollTop };
  host.replaceChildren();
  if (connected.length === 0 && free.length === 0) {
    const empty = document.createElement("div");
    empty.className = "empty";
    empty.textContent = nodes.length === 0 ? "No tasks" : "No active work";
    host.append(empty);
    return;
  }

  const canvas = document.createElement("div");
  canvas.className = "canvas";
  const graphSize = connected.length > 0 ? renderLinked(canvas, connected, links, depth, graph, highlight) : { width: 0, height: 0 };
  const groups = renderGroups(canvas, free, highlight, graphSize, { width: host.clientWidth, height: host.clientHeight - CONTROLS_BAND });
  canvas.style.width = `${Math.ceil(Math.max(graphSize.width, groups.width))}px`;
  canvas.style.height = `${Math.ceil(Math.max(graphSize.height, groups.height))}px`;
  host.append(canvas);
  host.scrollTo(scroll.left, scroll.top);
}

// renderLinked は辺でつながるノードを線と箱で canvas に描き、その大きさを返す。
function renderLinked(canvas, connected, links, depth, graph, highlight) {
  const { g, pos, back, width, height } = layout(connected, links, depth);

  const svg = document.createElementNS(SVG, "svg");
  svg.setAttribute("class", "edges");
  svg.setAttribute("width", String(Math.ceil(width)));
  svg.setAttribute("height", String(Math.ceil(height)));
  const defs = document.createElementNS(SVG, "defs");
  defs.append(arrowMarker("arrow", ""), arrowMarker("arrow-chain", "chain"));
  svg.append(defs);

  const chain = chainEdges(graph);
  const drawn = [...links.filter((l) => g.hasEdge(l.before, l.after)).map((l) => ({ ...l, points: g.edge(l.before, l.after).points })), ...back];
  const spread = spreadPorts(drawn, pos, NODE_W, NODE_H);
  const jumps = findJumps(spread.map((l) => l.points), { radius: JUMP_R, margin: JUMP_MARGIN });
  spread.forEach((l, i) => {
    const onChain = chain.has(`${l.before}\u0000${l.after}`);
    const cls = [l.kind === "task" ? "" : l.kind, onChain ? "chain" : ""].filter(Boolean).join(" ");
    const hint = l.kind === "folded" ? foldedHint(l) : (l.edge?.note ?? "");
    svg.append(edgePath(jumpPath(l.points, jumps[i], JUMP_R), cls, onChain ? "arrow-chain" : "arrow", hint));
  });
  canvas.append(svg);

  for (const n of connected) {
    const p = pos.get(n.key);
    const card = nodeCard(n, highlight);
    card.style.left = `${Math.round(p.x - NODE_W / 2)}px`;
    card.style.top = `${Math.round(p.y - NODE_H / 2)}px`;
    canvas.append(card);
  }
  return { width, height };
}

// renderGroups は辺の無い動いているノードをセッションの枠にして、線のあるかたまりの下に並べる。折り返しは、図全体を
// 見える領域 (view) に収める拡大率がいちばん大きくなる列の数で決める (fitLimit)。全体表示がオフでも同じ折り返しにし、
// 拡大縮小で並びを動かさない。枠が無ければ大きさは 0。
function renderGroups(canvas, free, highlight, graphSize, view) {
  if (free.length === 0) return { width: 0, height: 0 };
  const groups = groupBySession(free);
  const top = graphSize.height > 0 ? graphSize.height : MARGIN_Y;
  const limit = fitLimit(groups, { left: MARGIN_X, top, bottom: MARGIN_Y, graph: graphSize, view });
  const { places, right, bottom } = placeGroups(groups, MARGIN_X, top, limit);
  groups.forEach((g, i) => {
    const section = document.createElement("section");
    section.className = "group";
    section.setAttribute("aria-label", g.label);
    section.style.left = `${places[i].x}px`;
    section.style.top = `${places[i].y}px`;
    section.style.width = `${GROUP_W}px`;
    const head = document.createElement("h2");
    head.textContent = g.label;
    head.title = g.label;
    section.append(head, ...g.nodes.map((n) => nodeCard(n, highlight, true)));
    canvas.append(section);
  });
  return { width: right + MARGIN_X, height: bottom + MARGIN_Y };
}

// renderColumn は辺の無い終わったノードを右の列に縦 1 列で並べる。列は拡大縮小の外にあり、独立してスクロールする。
// 無ければ列ごと隠して、依存図に幅を返す。
function renderColumn(ended, highlight) {
  const host = el("column");
  const scroll = host.scrollTop;
  host.hidden = ended.length === 0;
  const head = document.createElement("h2");
  head.append(heading("Ended"), counter(ended.length));
  host.replaceChildren(head, ...ended.map((n) => nodeCard(n, highlight)));
  host.scrollTop = scroll;
}

// heading は見出しの語、counter はその横に置く件数の印。
function heading(text) {
  const ph = document.createElement("span");
  ph.className = "ph";
  ph.textContent = text;
  return ph;
}

function counter(n) {
  const c = document.createElement("span");
  c.className = "counter";
  c.textContent = String(n);
  return c;
}

function renderToggle() {
  const done = state.graph.nodes.filter((n) => n.state === "completed").length;
  el("completed-toggle").hidden = done === 0;
  el("completed-check").checked = state.showCompleted;
  el("completed-count").textContent = String(done);
}

// ---- メモの一覧 ----

function memoRow({ memo, repo }) {
  const b = document.createElement("button");
  b.type = "button";
  b.className = "mrow";
  if (state.selected?.type === "memo" && state.selected.id === memo.id) b.classList.add("sel");
  b.dataset.memo = memo.id;
  b.title = repo.title;
  b.addEventListener("click", (e) => {
    e.stopPropagation();
    select(state.selected?.type === "memo" && state.selected.id === memo.id ? null : { type: "memo", id: memo.id });
  });
  const k = document.createElement("span");
  k.className = "k";
  k.textContent = memo.kind;
  const t = document.createElement("span");
  t.className = "t";
  t.textContent = memo.sources.length > 1 ? `${memo.title} ×${memo.sources.length}` : memo.title;
  const s = document.createElement("span");
  s.className = "s";
  s.textContent = memo.sources[0]?.session ?? "";
  const w = document.createElement("span");
  w.className = "w";
  w.textContent = stamp(lastPlacedAt(memo));
  b.append(k, t, s, w);
  return b;
}

function renderPark() {
  const park = el("park");
  park.hidden = false;
  const total = state.data.repos.reduce((n, r) => n + r.memos.length, 0);

  // 一覧は見出しを持たない。件数は repo の絞り込みの選択肢に出る。
  const filter = el("repo-filter");
  const current = state.repoFilter;
  filter.replaceChildren();
  const all = document.createElement("option");
  all.value = "";
  all.textContent = "All repos";
  filter.append(all);
  for (const r of state.data.repos) {
    const o = document.createElement("option");
    o.value = r.repo;
    o.textContent = `${r.title} (${r.memos.length})`;
    filter.append(o);
  }
  filter.value = state.data.repos.some((r) => r.repo === current) ? current : "";
  state.repoFilter = filter.value;

  const list = el("park-list");
  list.replaceChildren();
  const rows = memos();
  if (rows.length === 0) {
    const none = document.createElement("div");
    none.className = "none";
    none.textContent = total === 0 ? "No open memos" : "No open memos in this repo";
    list.append(none);
    return;
  }
  for (const row of rows) list.append(memoRow(row));
}

// ---- 単体ビュー (ポップアップ) ----

// chip は枠だけのラベル。tone は色の鍵 (ok / warn / limit / err / merge / guess) で、無ければ灰の枠。
function chip(text, tone) {
  const c = document.createElement("span");
  c.className = tone ? `chip ${tone}` : "chip";
  const t = document.createElement("span");
  t.textContent = text;
  c.append(t);
  return c;
}

// group は関係の一覧。見出しの語の横に件数の印を置き、上を罫線で区切る。
function group(label, count, rows) {
  const g = document.createElement("div");
  g.className = "grp sep";
  const gh = document.createElement("div");
  gh.className = "gh";
  gh.append(heading(label), counter(count));
  g.append(gh, ...rows);
  return g;
}

// picker は動詞を開いた中身。閉じるのは押している動詞をもう一度押す (toggle) ので、やめるボタンは持たない。
function picker(parts) {
  const p = document.createElement("div");
  p.className = "picker";
  p.append(...parts);
  return p;
}

// rowList は見出しの付いた行の一覧 (.row を並べる)。
function rowList(label, items) {
  const box = document.createElement("div");
  box.className = "rows";
  box.append(heading(label), ...items);
  return box;
}

// rowButton は押すとそこへ渡る・足す行。lead は先頭の要素 (状態の印)、sub は名前の横の薄い補足。cls の end は補足を右端へ寄せる。
function rowButton({ title, sub, lead, cls, hint, onClick }) {
  const b = document.createElement("button");
  b.type = "button";
  b.className = cls ? `row ${cls}` : "row";
  b.disabled = state.busy;
  if (hint) b.title = hint;
  if (lead) b.append(lead);
  const t = document.createElement("span");
  t.className = "t";
  t.textContent = title;
  b.append(t);
  if (sub !== undefined) {
    const s = document.createElement("span");
    s.className = "s";
    s.textContent = sub;
    b.append(s);
  }
  b.addEventListener("click", onClick);
  return b;
}

// iconButton は文字を出さないアイコンのボタン。名前は aria-label と title で伝える。
function iconButton(icon, label, onClick) {
  const b = document.createElement("button");
  b.type = "button";
  b.className = "icon";
  b.setAttribute("aria-label", label);
  b.title = label;
  b.disabled = state.busy;
  // ICONS はコードの定数だけで、データの文字列は入れない。
  b.innerHTML = svg(icon);
  b.addEventListener("click", onClick);
  return b;
}

// stateMark は状態の印 (丸か、PR と issue の形)。
function stateMark(n) {
  const dot = document.createElement("span");
  dot.className = markClass(n);
  return dot;
}

// nodeRow は関係するノードの 1 行: 状態の丸、題 (押すとそのノードを選ぶ)、直接か間接かの印、依存を外すボタン (あれば)。
// owner は選んでいるノード (同じセッションの blockedBy の解消を頼む宛先)。
function nodeRow(n, mark, link, owner) {
  const row = document.createElement("div");
  row.className = "lk";
  const b = document.createElement("button");
  b.type = "button";
  b.textContent = n.title;
  // 題は note と並ぶと先頭だけ残して切れるので、全文を tooltip に持たせる。
  b.title = n.title;
  b.addEventListener("click", () => select({ type: "node", key: n.key }));
  row.append(stateMark(n), b, heading(mark));
  // edge add の note (何の待ちか) は行に添える。
  if (link?.edge?.note) {
    const note = document.createElement("span");
    note.className = "note";
    appendLinked(note, link.edge.note, linkContext(link.edge.after.repo, link.edge.after.cwd));
    row.append(note);
  }
  // `edge add` で書いた辺 (kind edge) は元の記録を消せる。同じセッションの blockedBy はそのセッションの TaskUpdate のもの。
  if (link?.kind === "edge" && link.edge) row.append(iconButton("unlink", "Unlink", () => act(() => unlink(link))));
  // 同じセッションの blockedBy は TaskUpdate のもので外せないので、そのセッションに解消を頼む。
  if (link?.kind === "task" && owner && canTell(owner)) row.append(iconButton("unlink", "Ask the session to unlink", () => act(() => askUnlink(n, owner))));
  return row;
}

// related は key の直近 (直接) と、その先 (間接) のノードを並べる。direct は [ノード, 辺] の組。
function related(g, direct, all, owner) {
  const rows = [];
  const seen = new Set();
  for (const [n, link] of direct) {
    seen.add(n.key);
    rows.push(nodeRow(n, "Direct", link, owner));
  }
  for (const k of all) {
    if (seen.has(k)) continue;
    const n = g.node(k);
    if (n) rows.push(nodeRow(n, "Indirect"));
  }
  return rows;
}

// アイコンは Octicons (GitHub、MIT。vendor/LICENSE.octicons) の 16px の path。右の語は Octicons の名前。
const ICONS = {
  send: '<path d="M.989 8 .064 2.68a1.342 1.342 0 0 1 1.85-1.462l13.402 5.744a1.13 1.13 0 0 1 0 2.076L1.913 14.782a1.343 1.343 0 0 1-1.85-1.463L.99 8Zm.603-5.288L2.38 7.25h4.87a.75.75 0 0 1 0 1.5H2.38l-.788 4.538L13.929 8Z"/>', // paper-airplane
  link: '<path d="m7.775 3.275 1.25-1.25a3.5 3.5 0 1 1 4.95 4.95l-2.5 2.5a3.5 3.5 0 0 1-4.95 0 .751.751 0 0 1 .018-1.042.751.751 0 0 1 1.042-.018 1.998 1.998 0 0 0 2.83 0l2.5-2.5a2.002 2.002 0 0 0-2.83-2.83l-1.25 1.25a.751.751 0 0 1-1.042-.018.751.751 0 0 1-.018-1.042Zm-4.69 9.64a1.998 1.998 0 0 0 2.83 0l1.25-1.25a.751.751 0 0 1 1.042.018.751.751 0 0 1 .018 1.042l-1.25 1.25a3.5 3.5 0 1 1-4.95-4.95l2.5-2.5a3.5 3.5 0 0 1 4.95 0 .751.751 0 0 1-.018 1.042.751.751 0 0 1-1.042.018 1.998 1.998 0 0 0-2.83 0l-2.5 2.5a1.998 1.998 0 0 0 0 2.83Z"/>', // link
  unlink: '<path d="M12.914 5.914a2 2 0 0 0-2.828-2.828l-.837.837a.75.75 0 1 1-1.06-1.061l.836-.837a3.5 3.5 0 1 1 4.95 4.95l-.195.194a.75.75 0 0 1-1.06-1.06l.194-.195Zm-1.87 3.482a.759.759 0 0 1-.07.079c-.63.63-1.468 1.108-2.343 1.263-.89.159-1.86-.017-2.606-.763a.75.75 0 1 1 1.06-1.06c.329.327.767.438 1.284.347.493-.088 1.018-.36 1.445-.752l-1.247-.897a.709.709 0 0 1-.01-.008l-.295-.212c-.94-.597-1.984-.499-2.676.193l-2.5 2.5a2 2 0 1 0 2.828 2.828l.837-.836a.75.75 0 0 1 1.06 1.06l-.836.837a3.5 3.5 0 0 1-4.95-4.95l2.5-2.5a3.472 3.472 0 0 1 1.354-.848L2.312 3.109a.75.75 0 0 1 .876-1.218l5.93 4.27c.115.074.226.155.335.24l6.235 4.49a.75.75 0 0 1-.876 1.218l-3.768-2.713Z"/>', // unlink
  hand: '<path d="M8.22 2.97a.75.75 0 0 1 1.06 0l4.25 4.25a.75.75 0 0 1 0 1.06l-4.25 4.25a.751.751 0 0 1-1.042-.018.751.751 0 0 1-.018-1.042l2.97-2.97H3.75a.75.75 0 0 1 0-1.5h7.44L8.22 4.03a.75.75 0 0 1 0-1.06Z"/>', // arrow-right
  play: '<path d="m6.427 4.427 3.396 3.396a.25.25 0 0 1 0 .354l-3.396 3.396A.25.25 0 0 1 6 11.396V4.604a.25.25 0 0 1 .427-.177Z"/>', // triangle-right
  check: '<path d="M13.78 4.22a.75.75 0 0 1 0 1.06l-7.25 7.25a.75.75 0 0 1-1.06 0L2.22 9.28a.751.751 0 0 1 .018-1.042.751.751 0 0 1 1.042-.018L6 10.94l6.72-6.72a.75.75 0 0 1 1.06 0Z"/>', // check
  memo: '<path d="M0 3.75C0 2.784.784 2 1.75 2h12.5c.966 0 1.75.784 1.75 1.75v8.5A1.75 1.75 0 0 1 14.25 14H1.75A1.75 1.75 0 0 1 0 12.25Zm1.75-.25a.25.25 0 0 0-.25.25v8.5c0 .138.112.25.25.25h12.5a.25.25 0 0 0 .25-.25v-8.5a.25.25 0 0 0-.25-.25ZM3.5 6.25a.75.75 0 0 1 .75-.75h7a.75.75 0 0 1 0 1.5h-7a.75.75 0 0 1-.75-.75Zm.75 2.25h4a.75.75 0 0 1 0 1.5h-4a.75.75 0 0 1 0-1.5Z"/>', // note
  trash: '<path d="M11 1.75V3h2.25a.75.75 0 0 1 0 1.5H2.75a.75.75 0 0 1 0-1.5H5V1.75C5 .784 5.784 0 6.75 0h2.5C10.216 0 11 .784 11 1.75ZM4.496 6.675l.66 6.6a.25.25 0 0 0 .249.225h5.19a.25.25 0 0 0 .249-.225l.66-6.6a.75.75 0 0 1 1.492.149l-.66 6.6A1.748 1.748 0 0 1 10.595 15h-5.19a1.75 1.75 0 0 1-1.741-1.575l-.66-6.6a.75.75 0 1 1 1.492-.15ZM6.5 1.75V3h3V1.75a.25.25 0 0 0-.25-.25h-2.5a.25.25 0 0 0-.25.25Z"/>', // trash
  remove: '<path d="M8 0a8 8 0 1 1 0 16A8 8 0 0 1 8 0ZM3.965 13.096a6.5 6.5 0 0 0 9.131-9.131ZM1.5 8a6.474 6.474 0 0 0 1.404 4.035l9.131-9.131A6.499 6.499 0 0 0 1.5 8Z"/>', // circle-slash
  task: '<path d="M2 2h4a1 1 0 0 1 1 1v4a1 1 0 0 1-1 1H2a1 1 0 0 1-1-1V3a1 1 0 0 1 1-1Zm4.655 8.595a.75.75 0 0 1 0 1.06L4.03 14.28a.75.75 0 0 1-1.06 0l-1.5-1.5a.749.749 0 0 1 .326-1.275.749.749 0 0 1 .734.215l.97.97 2.095-2.095a.75.75 0 0 1 1.06 0ZM9.75 2.5h5.5a.75.75 0 0 1 0 1.5h-5.5a.75.75 0 0 1 0-1.5Zm0 5h5.5a.75.75 0 0 1 0 1.5h-5.5a.75.75 0 0 1 0-1.5Zm0 5h5.5a.75.75 0 0 1 0 1.5h-5.5a.75.75 0 0 1 0-1.5Zm-7.25-9v3h3v-3Z"/>', // tasklist
  issue: '<path d="M8 9.5a1.5 1.5 0 1 0 0-3 1.5 1.5 0 0 0 0 3Z"/><path d="M8 0a8 8 0 1 1 0 16A8 8 0 0 1 8 0ZM1.5 8a6.5 6.5 0 1 0 13 0 6.5 6.5 0 0 0-13 0Z"/>', // issue-opened
  close: '<path d="M3.72 3.72a.75.75 0 0 1 1.06 0L8 6.94l3.22-3.22a.749.749 0 0 1 1.275.326.749.749 0 0 1-.215.734L9.06 8l3.22 3.22a.749.749 0 0 1-.326 1.275.749.749 0 0 1-.734-.215L8 9.06l-3.22 3.22a.751.751 0 0 1-1.042-.018.751.751 0 0 1-.018-1.042L6.94 8 3.72 4.78a.75.75 0 0 1 0-1.06Z"/>', // x
};

function svg(icon) {
  return `<svg viewBox="0 0 16 16" aria-hidden="true">${ICONS[icon]}</svg>`;
}

// actButton は動詞のボタン。アイコンの下に短い語を置く。tone の on は開いている動詞で、aria-pressed でも伝える。
function actButton(label, icon, tone, onClick) {
  const b = document.createElement("button");
  b.type = "button";
  b.className = tone ? `act ${tone}` : "act";
  b.disabled = state.busy;
  if (tone.split(" ").includes("on")) b.setAttribute("aria-pressed", "true");
  // ICONS はコードの定数だけで、データの文字列は入れない。
  b.innerHTML = svg(icon);
  b.append(document.createTextNode(label));
  b.addEventListener("click", onClick);
  return b;
}

function actsBar(buttons) {
  const bar = document.createElement("div");
  bar.className = "acts";
  bar.append(...buttons);
  return bar;
}

// blockerPicker は先に済ませるタスクの候補の一覧。行を押すとその辺を書く。
function blockerPicker(n) {
  const items = candidates(state.graph, n).map((c) => rowButton({ title: c.title, sub: c.session ?? "", lead: stateMark(c), cls: "end", hint: c.title, onClick: () => act(() => addBlocker(c, n)) }));
  // 端点にできる、完了していないタスクが他に無い。
  if (items.length === 0) items.push(heading("No candidates"));
  return picker([rowList("Add blocker", items)]);
}

// composer は枠の中に入力欄と、押しても送れるキーの印 (⌘↵) を並べる欄。key は下書きを持つ state の名前 (draft か note)。
// submit が無ければ印を出さない (行を押して送る操作の補足)。needsText は空欄では送らない。textarea なので IME の変換中の
// Enter は送信にならない。
function composer({ placeholder, key, rows, submit, label, needsText }) {
  const box = document.createElement("div");
  box.className = "composer";
  const t = document.createElement("textarea");
  t.rows = rows;
  t.placeholder = placeholder;
  t.setAttribute("aria-label", placeholder);
  t.value = state[key];
  box.append(t);
  const ready = () => !state.busy && (!needsText || state[key].trim() !== "");
  let send = null;
  t.addEventListener("input", () => {
    state[key] = t.value;
    if (send) send.disabled = !ready();
  });
  if (submit === undefined) return box;
  send = document.createElement("button");
  send.type = "button";
  send.className = "key send";
  send.textContent = "⌘↵";
  send.setAttribute("aria-label", label);
  send.title = label;
  send.disabled = !ready();
  const run = () => {
    if (ready()) submit();
  };
  send.addEventListener("click", run);
  t.addEventListener("keydown", (e) => {
    if (e.key === "Enter" && e.metaKey && !e.isComposing) {
      e.preventDefault();
      run();
    }
  });
  const keys = document.createElement("div");
  keys.className = "keys";
  keys.append(send);
  box.append(keys);
  return box;
}

// noteField は操作の文面の末尾に添える補足欄。送るのは宛先の行を押したとき。
function noteField() {
  return composer({ placeholder: "Note (optional)", key: "note", rows: 2 });
}

// refusalLine は送達が拒まれた理由。無ければ出さない。
function refusalLine() {
  const p = document.createElement("div");
  p.className = "refusal";
  p.textContent = state.refusal;
  p.hidden = state.refusal === "";
  return p;
}

// destinationRows は生きているセッションの宛先の一覧。名前の横に今の作業を薄く出す。無ければその旨を 1 行出す。
function destinationRows(peers, onPick) {
  const items = peers.map((to) => rowButton({ title: to.session, sub: to.subject, hint: to.session_id, onClick: () => onPick(to) }));
  if (items.length === 0) items.push(heading("No live sessions"));
  return rowList("Sessions", items);
}

// SPAWN_SHOWN は起こす repo の候補を、「ほか n 件」を押す前に出す件数。台帳の repo は多いので畳む。
const SPAWN_SHOWN = 8;

// spawnRows は「新しいセッションを起こして渡す」の repo の一覧とフォルダの選択。first は先頭に出す repo (無ければ undefined)。
function spawnRows(first, onPick) {
  const now = Date.now();
  const all = spawnDestinations(state.data, first);
  const shown = state.moreRepos ? all : all.slice(0, SPAWN_SHOWN);
  // repo の行は名前と、最後に見た時刻からの経過を右端に分けて出す (時刻が無ければ名前だけ)。
  const items = shown.map((to) => {
    const seen = lastSeenAgo(to, now);
    return rowButton({ title: to.title, ...(seen === "" ? {} : { sub: seen }), cls: "end", hint: to.repo, onClick: () => onPick(to) });
  });
  if (all.length > shown.length) {
    const more = rowButton({
      title: `${all.length - shown.length} more…`,
      cls: "more",
      onClick: () => {
        state.moreRepos = true;
        renderPopup();
      },
    });
    more.disabled = false;
    items.push(more);
  }
  items.push(
    rowButton({
      title: "Choose folder…",
      cls: "more",
      onClick: async () => {
        const path = await pickFolder();
        if (path === "") return;
        const to = pathDestination(path);
        if ("failure" in to) {
          state.notice = to.failure;
          state.noticeUntil = Date.now() + NOTICE_MS;
          renderFoot();
          return;
        }
        await onPick(to);
      },
    }),
  );
  return rowList("New session", items);
}

// repoOf はノードのセッションがいる repo (起こす repo の候補の先頭にする)。
function repoOf(n) {
  const r = state.data.repos.find((repo) => repo.sessions.some((s) => s.session_id === n.session_id));
  return r ? { repo: r.repo, title: r.title } : undefined;
}

// memoHandPicker は「タスクにする」「issue にする」の宛先: 生きているセッションと、新しく起こす repo。
function memoHandPicker(memo, repo, intent) {
  const peers = intent === "task" ? taskDestinations(repo) : issueDestinations(state.data, repo);
  const pick = (to) => act(() => handOff(memo, to, intent, state.note));
  return picker([noteField(), refusalLine(), destinationRows(peers, pick), spawnRows({ repo: repo.repo, title: repo.title }, pick)]);
}

// taskHandPicker は「渡す」の宛先: 持ち主を除く生きているセッションと、新しく起こす repo。
function taskHandPicker(n) {
  const pick = (to) => act(() => handOffTask(n, to, state.note));
  return picker([noteField(), refusalLine(), destinationRows(peerDestinations(state.data, n.session_id), pick), spawnRows(repoOf(n), pick)]);
}

// instructPicker は指示の文面を打って送る。宛先は見出しのセッションの chip が示すので、欄だけを出す。
function instructPicker(n) {
  const submit = () => act(() => instruct(n, state.draft.trim()));
  return picker([composer({ placeholder: "Instruction", key: "draft", rows: 3, submit, label: "Send", needsText: true }), refusalLine()]);
}

// startPicker は着手の依頼に補足を添えて送る。補足は空でも頼める。
function startPicker(n) {
  const submit = () => act(() => start(n, state.note));
  return picker([composer({ placeholder: "Note (optional)", key: "note", rows: 2, submit, label: "Ask to start" }), refusalLine()]);
}

// toggle は picker を開く・閉じる動詞。
// renameField は単体ビューの題をその場で打ち直す欄 (題の位置に置く)。空欄では直さない。Enter で直し、Escape で戻す。
function renameField(n) {
  const wrap = document.createElement("span");
  wrap.className = "edit";
  const t = document.createElement("input");
  t.type = "text";
  t.setAttribute("aria-label", "Work title");
  t.value = state.draft === "" ? n.title : state.draft;
  // 直すのは Enter、戻すのは Escape。枠の中の印は押しても直せる。
  const save = document.createElement("button");
  save.type = "button";
  save.className = "key";
  save.textContent = "↵";
  save.setAttribute("aria-label", "Rename");
  save.title = "Rename (Enter), cancel (Esc)";
  const submit = () => {
    const subject = t.value.trim();
    if (subject === "") return;
    act(() => rename(n, subject));
  };
  save.disabled = state.busy;
  save.addEventListener("click", submit);
  t.addEventListener("input", () => {
    state.draft = t.value;
  });
  t.addEventListener("keydown", (e) => {
    if (e.isComposing) return;
    if (e.key === "Enter") {
      e.preventDefault();
      submit();
    } else if (e.key === "Escape") {
      e.stopPropagation();
      toggle("rename")();
    }
  });
  wrap.append(t, save);
  return wrap;
}

// titleOf は単体ビューの題。題を直せるノード (canRename) では題そのものが入口で、押すとその場で欄に変わる (OOUI:
// 対象を選んでから動詞。動詞のバーに「題を直す」は置かない)。
function titleOf(n) {
  const title = document.createElement("span");
  title.className = "title";
  if (!canRename(n)) {
    title.textContent = n.title;
    return title;
  }
  if (state.picker === "rename") {
    title.classList.add("editing");
    title.append(renameField(n));
    return title;
  }
  title.classList.add("editable");
  title.textContent = n.title;
  title.title = "Click to rename (a renamed title is not overwritten automatically)";
  title.setAttribute("role", "button");
  title.tabIndex = 0;
  title.addEventListener("click", toggle("rename"));
  title.addEventListener("keydown", (e) => {
    if (e.key === "Enter" || e.key === " ") {
      e.preventDefault();
      toggle("rename")();
    }
  });
  return title;
}

function toggle(name) {
  return () => {
    state.picker = state.picker === name ? null : name;
    state.refusal = "";
    renderPopup();
  };
}

function on(name) {
  return state.picker === name ? "on" : "";
}

// toneChip は状態のラベル。丸の色の鍵をラベルの鍵に写す (作業中は ok、終了と取りやめは灰の枠)。
function toneChip(text, tone) {
  const chipTone = tone === "working" ? "ok" : tone === "exited" || tone === "drop" ? "" : tone ?? "";
  return chip(text, chipTone);
}

function stateChip(n) {
  return toneChip(stateLabel(n), toneOf(n));
}

// restStageChips は箱に出さなかった PR の段の chip。stateChip が先頭の段を出す。
function restStageChips(n) {
  return (n.pr_stages ?? []).slice(1).flatMap((s) => (PR_STAGE[s] ? [toneChip(PR_STAGE[s].label, PR_STAGE[s].tone)] : []));
}

function nodePopup(n) {
  const g = state.graph;
  const parts = [];
  const head = document.createElement("div");
  head.className = "head";
  const ph = document.createElement("span");
  ph.className = "ph";
  ph.textContent = [KIND_LABEL[n.kind] ?? n.kind, n.task_id].filter(Boolean).join(" · ");
  const title = titleOf(n);
  const chips = document.createElement("div");
  chips.className = "chips";
  chips.append(stateChip(n), ...restStageChips(n));
  // 会話から自動で付けた題は、状態の色と被らない破線の chip (guess) で出どころを示す。直すのは題を押す。
  if (n.origin === "inferred") chips.append(chip(INFERRED_CHIP, "guess"));
  if (n.session) chips.append(chip(n.session));
  head.append(ph, title, chips);
  parts.push(head);

  // 動詞。送達を伴うもの (指示、渡す、着手、完了、削除、メモにする) は生きているセッションのタスクに、バイナリだけで済む
  // もの (メモにする = adopt、一覧から外す) は終了したセッションのタスクと作業に出る。題を直すのは題そのもの (titleOf)。
  const verbs = [];
  if (canTell(n)) verbs.push(actButton("Tell", "send", on("instruct"), toggle("instruct")));
  if (n.endpoint && n.cwd !== undefined) verbs.push(actButton("Link", "link", on("blocker"), toggle("blocker")));
  if (isOwnTask(n)) {
    verbs.push(actButton("Hand", "hand", on("hand"), toggle("hand")));
    if (n.state !== "in_progress") verbs.push(actButton("Start", "play", on("start"), toggle("start")));
    verbs.push(actButton("Done", "check", "", () => act(() => setStatus(n, "completed"))));
    verbs.push(actButton("Memo", "memo", "", () => act(() => toParking(n))));
    verbs.push(actButton("Delete", "trash", "danger", () => act(() => setStatus(n, "deleted"))));
  }
  if (canAdopt(n, state.data)) verbs.push(actButton("Memo", "memo", "", () => act(() => adopt(n))));
  if (canDismiss(n, state.data)) verbs.push(actButton("Remove", "remove", "danger", () => act(() => dismissOne(n))));
  if (verbs.length > 0) parts.push(actsBar(verbs));
  if (state.picker === "instruct") parts.push(instructPicker(n));
  if (state.picker === "blocker") parts.push(blockerPicker(n));
  if (state.picker === "hand") parts.push(taskHandPicker(n));
  if (state.picker === "start") parts.push(startPicker(n));
  if (state.refusal !== "" && !["instruct", "blocker", "hand", "start"].includes(state.picker)) parts.push(refusalLine());

  // タスクの説明と並ぶので、今の作業 (msg) は見出しを添えて分ける。
  for (const text of [n.description, n.summary ? `Now: ${n.summary}` : ""]) {
    if (!text) continue;
    const p = document.createElement("p");
    appendLinked(p, text, linkContext(n.repo, n.cwd));
    parts.push(p);
  }

  // Blocks はこのノードを待っているもの、Blocked by はこのノードが待っているもの。
  const after = g.waiters(n.key).flatMap((l) => (g.node(l.after) ? [[g.node(l.after), l]] : []));
  parts.push(group("Blocks", g.reach(n.key).size, related(g, after, g.reach(n.key))));
  const before = g.blockers(n.key).flatMap((l) => (g.node(l.before) ? [[g.node(l.before), l]] : []));
  parts.push(group("Blocked by", g.upstream(n.key).size, related(g, before, g.upstream(n.key), n)));

  if (n.memos.length > 0) {
    parts.push(group("Memos handled", n.memos.length, n.memos.map((m) => memoRowInPopup(m.id, m.title, m.status, m.status === "open"))));
  }
  // 途中で置いたメモは open のものだけが repos に載る (parked の数と同じ)。
  const parked = memos().filter(({ memo }) => memo.sources.some((s) => s.task_ref === n.key));
  if (parked.length > 0) {
    parts.push(group("Memos placed", parked.length, parked.map(({ memo }) => memoRowInPopup(memo.id, memo.title, memo.kind, true))));
  }
  return parts;
}

// memoRowInPopup はタスクの単体ビューに並ぶメモの 1 行。open のメモは押すとそのメモを選ぶ。
function memoRowInPopup(id, title, mark, selectable) {
  const row = document.createElement("div");
  row.className = "lk";
  const b = document.createElement("button");
  b.type = "button";
  b.textContent = title;
  b.title = title;
  b.disabled = !selectable;
  b.addEventListener("click", () => select({ type: "memo", id }));
  row.append(b, chip(mark, "guess"));
  return row;
}

function memoPopup({ memo, repo }) {
  const g = state.graph;
  const parts = [];
  const head = document.createElement("div");
  head.className = "head";
  const ph = document.createElement("span");
  ph.className = "ph";
  // 一覧の行を押して開くので、メモであることは書かない。repo と種類だけを出す。
  ph.textContent = `${repo.title} · ${kindLabel(memo)}`;
  const title = document.createElement("span");
  title.className = "title";
  title.textContent = memo.title;
  const chips = document.createElement("div");
  chips.className = "chips";
  chips.append(chip("open", "ok"));
  head.append(ph, title, chips);
  parts.push(head);

  // 動詞。渡す先は生きているセッションか、新しく起こすセッション。
  parts.push(
    actsBar([
      actButton("Task", "task", `pri ${on("task")}`.trim(), toggle("task")),
      actButton("Issue", "issue", on("issue"), toggle("issue")),
      actButton("Discard", "trash", "danger", () => act(() => discard(memo))),
    ]),
  );
  if (state.picker === "task" || state.picker === "issue") parts.push(memoHandPicker(memo, repo, state.picker));
  else if (state.refusal !== "") parts.push(refusalLine());

  if (memo.detail) {
    const p = document.createElement("p");
    appendLinked(p, memo.detail, linkContext(repo.repo, repo.repo));
    parts.push(p);
  }
  // 既定・ファイル・出どころを 1 つの表にする。From は置いたセッション、そのときのタスク (押すと選ぶ)、補足、時刻で、
  // 置いた回数だけ新しい順に並ぶ (見出しは最初の行だけ)。
  const dl = document.createElement("dl");
  dl.className = "kv";
  const add = (k, ...v) => {
    const dt = document.createElement("dt");
    dt.textContent = k;
    const dd = document.createElement("dd");
    dd.append(...v);
    dl.append(dt, dd);
  };
  if (memo.default_taken) add("Default", memo.default_taken);
  if (memo.files?.length) add("Files", memo.files.join(", "));
  memo.sources.forEach((s, i) => {
    const task = s.task_ref ? g.node(s.task_ref) : undefined;
    const cells = [s.session + (s.live ? "" : " (ended)")];
    if (task) {
      const a = document.createElement("a");
      a.className = "ref";
      a.href = "#";
      a.textContent = task.title;
      a.addEventListener("click", (e) => {
        e.preventDefault();
        select({ type: "node", key: task.key });
      });
      cells.push(a);
    }
    if (s.detail) cells.push(s.detail);
    cells.push(stamp(s.at));
    add(i === 0 ? "From" : "", ...cells.flatMap((c, j) => (j === 0 ? [c] : [" · ", c])));
  });
  parts.push(dl);
  return parts;
}

// placePopup はポップアップを anchor (選んだ箱か行) のそばに置く。箱なら右 (入らなければ左)、行なら上 (入らなければ下)。
function placePopup(pop, anchor, side) {
  const r = anchor.getBoundingClientRect();
  const vw = window.innerWidth;
  const vh = window.innerHeight;
  const h = pop.offsetHeight;
  let left;
  let top;
  if (side === "beside") {
    left = r.right + 16;
    if (left + POP_W > vw - 12) left = r.left - 16 - POP_W;
    top = r.top - 16;
  } else {
    left = r.left + 40;
    top = r.top - h - 12;
    if (top < 60) top = r.bottom + 12;
  }
  // footer (28px) は隠さない。操作の結果がそこに出る。
  left = Math.max(12, Math.min(left, vw - POP_W - 12));
  top = Math.max(60, Math.min(top, vh - h - 40));
  pop.style.left = `${Math.round(left)}px`;
  pop.style.top = `${Math.round(top)}px`;
}

function renderPopup() {
  const pop = el("pop");
  let parts = null;
  let anchor = null;
  let side = "beside";
  const n = selectedNode();
  const m = selectedMemo();
  if (n) {
    anchor = document.querySelector(`.node[data-key="${CSS.escape(n.key)}"]`);
    parts = nodePopup(n);
    pop.setAttribute("aria-label", "Task details");
  } else if (m) {
    anchor = document.querySelector(`.mrow[data-memo="${CSS.escape(m.memo.id)}"]`);
    parts = memoPopup(m);
    side = "above";
    pop.setAttribute("aria-label", "Memo details");
  }
  if (parts === null || anchor === null) {
    // 選んだものが消えた (完了して隠れた、メモが渡された、repo の絞り込みで外れた) なら選択を解く。
    state.selected = null;
    pop.hidden = true;
    pop.replaceChildren();
    return;
  }
  const x = document.createElement("button");
  x.type = "button";
  x.className = "x";
  x.setAttribute("aria-label", "Close");
  x.title = "Close (Esc)";
  // ICONS はコードの定数だけで、データの文字列は入れない。
  x.innerHTML = svg("close");
  x.addEventListener("click", () => select(null));
  pop.replaceChildren(x, ...parts);
  pop.hidden = false;
  placePopup(pop, anchor, side);
}

// ---- footer ----

function relative(from) {
  const sec = Math.max(0, Math.round((Date.now() - from.getTime()) / 1000));
  if (sec < 60) return `${sec}s ago`;
  const min = Math.round(sec / 60);
  if (min < 60) return `${min}m ago`;
  return `${Math.round(min / 60)}h ago`;
}

function renderFoot() {
  const dot = el("foot").querySelector(".dot");
  const text = el("foot-text");
  if (state.data === null) {
    dot.className = "dot err";
    text.className = "err-text";
    text.textContent = state.error;
    return;
  }
  const d = state.data;
  const nodes = d.graph.nodes;
  const count = (f) => nodes.filter(f).length;
  const memoCount = d.repos.reduce((n, r) => n + r.memos.length, 0);
  const sessions = new Set(d.repos.flatMap((r) => r.sessions.map((s) => s.session_id))).size;
  const parts = [
    d.daemon_running ? "daemon running" : "daemon stopped",
    state.readAt ? `read ${relative(state.readAt)}` : "",
    "·",
    `Stuck ${count((n) => n.stuck)} · Working ${count((n) => n.state === "working")} · Waiting ${count((n) => n.state === "done" || n.state === "unknown")} · Completed ${count((n) => n.state === "completed")}${state.showCompleted ? "" : " (hidden)"}`,
    "·",
    `Tasks ${count((n) => n.kind === "task")} · Memos ${memoCount} · Sessions ${sessions}`,
  ];
  if (state.error) parts.push("·", `Reload failed: ${state.error}`);
  dot.className = d.daemon_running ? "dot working" : "dot";
  text.className = state.error ? "err-text" : "";
  // 操作の結果はしばらく footer を占め、その後に件数へ戻る。
  const noticed = state.notice !== "" && Date.now() < state.noticeUntil;
  text.textContent = noticed ? state.notice : parts.filter(Boolean).join(" ");
  if (noticed) text.className = "notice";
}

// renderBulk はまとめて行う操作のボタン: 終了したセッションのタスクを外す、古いメモを捨てる。該当があるときだけ出す。
function renderBulk() {
  const ended = visibleNodes().filter((n) => bulkDismissable(n, state.graph, state.data));
  const dismissBtn = el("dismiss-ended");
  dismissBtn.hidden = ended.length === 0;
  dismissBtn.textContent = `Dismiss ended (${ended.length})`;
  dismissBtn.title = "Remove the tasks and work of ended sessions from the list";
  dismissBtn.disabled = state.busy;
  dismissBtn.onclick = () => act(() => dismissAll(ended));

  const days = state.data.stale_memo_days;
  const stale = state.data.repos.flatMap((r) => r.memos).filter((m) => isStale(m, Date.now(), days)).length;
  const staleBtn = el("discard-stale");
  staleBtn.hidden = stale === 0;
  staleBtn.textContent = `Discard stale (${stale})`;
  staleBtn.title = `Discard open memos older than ${days} days`;
  staleBtn.disabled = state.busy;
  staleBtn.onclick = () => act(() => discardStale(days));
}

// ---- 全体 ----

function render() {
  if (state.data === null) {
    el("empty").textContent = state.error ? `Cannot read deps: ${state.error}` : "Reading deps…";
    renderFoot();
    return;
  }
  renderGraph();
  if (fitOn) fitAll(false);
  else applyZoom();
  renderToggle();
  el("legend").hidden = false;
  renderPark();
  renderBulk();
  // 補足欄を打っている最中はポップアップを組み直さない (focus が外れる)。位置だけ追わせる。
  const pop = el("pop");
  if (pop.contains(document.activeElement) && document.activeElement.tagName === "TEXTAREA") {
    const anchor = popupAnchor();
    if (anchor) placePopup(pop, anchor, state.selected.type === "node" ? "beside" : "above");
  } else {
    renderPopup();
  }
  renderFoot();
}

// ---- 拡大縮小 ----
// 描画には触れず、描いた canvas を枠で包んで scale とスクロール量だけを変える。枠が #graph (右の列を除いた領域) より
// 小さい向きは、枠に余白を付けて中心へ寄せる。

let zoom = 1;
// fitOn は「全体を表示」のトグル。オンの間は描き直すたびと窓の大きさが変わるたびに全体が収まる拡大率へ合わせ、
// 利用者が拡大縮小したらオフにする。
let fitOn = true;

function canvasSize(canvas) {
  return { width: parseFloat(canvas.style.width), height: parseFloat(canvas.style.height) };
}

// applyZoom は renderGraph が作った canvas を枠で包み、canvas に scale を、枠に拡大率を掛けた大きさと中心へ寄せる
// 余白を当てる。枠は canvas の作り直しで消え、余白は窓の大きさで変わるので、描くたびと窓の大きさが変わるたびに呼ぶ。
function applyZoom() {
  const host = el("graph");
  const canvas = host.querySelector(".canvas");
  if (canvas === null) return;
  let frame = canvas.parentElement;
  if (!frame.classList.contains("zoom-frame")) {
    frame = document.createElement("div");
    frame.className = "zoom-frame";
    canvas.replaceWith(frame);
    frame.append(canvas);
  }
  const size = canvasSize(canvas);
  const width = scaledFrame(size.width, zoom);
  const height = scaledFrame(size.height, zoom);
  frame.style.width = `${width}px`;
  frame.style.height = `${height}px`;
  frame.style.marginLeft = `${centerOffset(width, host.clientWidth)}px`;
  frame.style.marginTop = `${topOffset(height, host.clientHeight)}px`;
  canvas.style.transform = zoom === 1 ? "" : `scale(${zoom})`;
  // カードの強調の枠を縮小しても細くしすぎないよう、style.css が拡大率で割る。
  canvas.style.setProperty("--zoom", String(zoom));
}

// setZoom は拡大率を next にし、pointer (#graph の左上からの距離) が指す図の点を同じ画面位置に残す。
// pointer が無ければ見えている領域の中央を残す。
function setZoom(next, pointer) {
  const host = el("graph");
  const canvas = host.querySelector(".canvas");
  const to = clampZoom(next);
  if (canvas === null) {
    zoom = to;
    renderZoom();
    return;
  }
  const size = canvasSize(canvas);
  const p = pointer ?? { x: host.clientWidth / 2, y: host.clientHeight / 2 };
  const left = scrollAfterZoom(host.scrollLeft, p.x, zoom, to, size.width, host.clientWidth);
  const top = scrollAfterZoom(host.scrollTop, p.y, zoom, to, size.height, host.clientHeight, topOffset);
  zoom = to;
  applyZoom();
  host.scrollTo(left, top);
  renderZoom();
  if (state.selected !== null) renderPopup();
}

// zoomByUser は利用者の拡大縮小 (+ / −、100%、Cmd + ホイール、ピンチ)。全体表示をオフにしてから拡大率を変える。
function zoomByUser(next, pointer) {
  fitOn = false;
  setZoom(next, pointer);
}

// fitAll は図全体が見える領域に収まる拡大率にする (100% を超えて拡大せず、下限で止める)。拡大率が変わったときと
// force のときは、下限で止まってはみ出した分のうち上 (根の側) と横の中心を見せる。拡大率の変わらない描き直しでは
// 今のスクロールを保ち、数秒ごとの読み直しで位置を揺らさない。
function fitAll(force) {
  const host = el("graph");
  const canvas = host.querySelector(".canvas");
  if (canvas === null) return;
  const size = canvasSize(canvas);
  const next = fitZoom(size.width, size.height, host.clientWidth, host.clientHeight - CONTROLS_BAND);
  const changed = next !== zoom;
  zoom = next;
  applyZoom();
  if (changed || force) host.scrollTo(overflowCenter(scaledFrame(size.width, zoom), host.clientWidth), 0);
  renderZoom();
}

function renderZoom() {
  el("zoom-reset").textContent = percent(zoom);
  el("zoom-in").disabled = zoom >= MAX_ZOOM;
  el("zoom-out").disabled = zoom <= MIN_ZOOM;
  const fit = el("zoom-fit");
  fit.classList.toggle("on", fitOn);
  fit.setAttribute("aria-pressed", String(fitOn));
}

function pointerIn(e) {
  const r = el("graph").getBoundingClientRect();
  return { x: e.clientX - r.left, y: e.clientY - r.top };
}

el("zoom-in").addEventListener("click", () => zoomByUser(stepZoom(zoom, 1)));
el("zoom-out").addEventListener("click", () => zoomByUser(stepZoom(zoom, -1)));
el("zoom-reset").addEventListener("click", () => zoomByUser(1));
// オフにするときは今の拡大率と位置を残す。
el("zoom-fit").addEventListener("click", () => {
  fitOn = !fitOn;
  if (fitOn) fitAll(true);
  else renderZoom();
  if (state.selected !== null) renderPopup();
});
// Cmd + ホイール (と Chromium 系のピンチが送る ctrl + ホイール)。passive にすると preventDefault が効かずスクロールされる。
const wheelZoom = wheelZoomer();
el("graph").addEventListener("wheel", (e) => {
  const next = wheelZoom({ deltaY: e.deltaY, modifier: e.metaKey || e.ctrlKey, t: e.timeStamp }, zoom);
  if (next === null) return;
  e.preventDefault();
  zoomByUser(next, pointerIn(e));
}, { passive: false });
// WKWebView のトラックパッドのピンチは gesture* で届き、scale は gesturestart からの倍率。
let gestureFrom = 1;
el("graph").addEventListener("gesturestart", (e) => {
  e.preventDefault();
  gestureFrom = zoom;
});
el("graph").addEventListener("gesturechange", (e) => {
  e.preventDefault();
  zoomByUser(gestureFrom * e.scale, pointerIn(e));
});

function popupAnchor() {
  if (state.selected?.type === "node") return document.querySelector(`.node[data-key="${CSS.escape(state.selected.key)}"]`);
  if (state.selected?.type === "memo") return document.querySelector(`.mrow[data-memo="${CSS.escape(state.selected.id)}"]`);
  return null;
}

el("completed-check").addEventListener("change", (e) => {
  state.showCompleted = e.target.checked;
  render();
});
el("repo-filter").addEventListener("change", (e) => {
  state.repoFilter = e.target.value;
  render();
});
// 余白を押したら選択を解く。箱と行は stopPropagation で止める。
el("graph").addEventListener("click", () => select(null));
el("column").addEventListener("click", () => select(null));
el("park-list").addEventListener("click", () => select(null));
el("pop").addEventListener("click", (e) => e.stopPropagation());
document.addEventListener("keydown", (e) => {
  if (e.key === "Escape" && state.selected !== null) select(null);
});
// スクロールと窓の大きさの変化でポップアップの位置を追わせる。
for (const target of [el("graph"), el("column"), el("park-list")]) target.addEventListener("scroll", () => { if (state.selected !== null) renderPopup(); });
window.addEventListener("resize", () => {
  // 枠の折り返しは見える領域の幅で決まるので、描き直す。
  if (state.data !== null) renderGraph();
  if (fitOn) fitAll(false);
  else applyZoom();
  if (state.selected !== null) renderPopup();
});
document.addEventListener("visibilitychange", () => {
  if (!document.hidden) fetchDeps();
});

// 新しいバージョンへ切り替えるときに引き継ぐ画面の状態 (restore.js)。
const graphScroll = scrollView(el("graph"));
const views = {
  selected: { get: () => state.selected, set: (v) => { state.selected = v; } },
  showCompleted: { get: () => state.showCompleted, set: (v) => { state.showCompleted = v === true; } },
  repoFilter: { get: () => state.repoFilter, set: (v) => { state.repoFilter = String(v); } },
  // 開いていた選択と、打ちかけの指示と補足。
  picker: { get: () => state.picker, set: (v) => { state.picker = typeof v === "string" ? v : null; } },
  draft: { get: () => state.draft, set: (v) => { state.draft = String(v); } },
  note: { get: () => state.note, set: (v) => { state.note = String(v); } },
  zoom: { get: () => zoom, set: (v) => { zoom = clampZoom(Number(v) || 1); renderZoom(); } },
  // 全体表示がオンなら拡大率と位置は描き直しの fitAll が決めるので、図のスクロールは戻さない。戻すのは描いた後
  // (requestAnimationFrame) で fitAll の位置を上書きしてしまうため。apply は保存した鍵の順 (= この並び) に戻すので、
  // fitAll を graphScroll より前に置く。
  fitAll: { get: () => fitOn, set: (v) => { fitOn = v === true; renderZoom(); } },
  graphScroll: { get: graphScroll.get, set: (v) => { if (!fitOn) graphScroll.set(v); } },
  columnScroll: scrollView(el("column")),
  parkScroll: scrollView(el("park-list")),
};

await fetchDeps();
await restore(views);
render();
schedule();
watchUpgrade(views, el("upgrade"));
