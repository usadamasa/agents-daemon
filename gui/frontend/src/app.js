// 依存図の描画。`agents-daemon deps` の JSON (protocol.schema.json の DepsData) を Go 側の Daemon.Deps で読み、
// dagre で配置して箱 (HTML) と線 (SVG) を描く。段と下流の数と原因の鎖はバイナリが数えて渡すので、ここは並べるだけ。
// オブジェクトはタスク (図の箱) とメモ (下の一覧の行)。選ぶとそのそばにポップアップで単体ビューが開く。
import * as dagre from "./vendor/dagre.esm.js";
import { Graph } from "./graph.js";
import { spreadPorts } from "./ports.js";
import { isStale, kindLabel, lastPlacedAt, stamp } from "./memo.js";
import { canAdopt, canDismiss, canTell, candidates, destinationLabel, isOwnTask, issueDestinations, pathDestination, peerDestinations, spawnDestinations, spawnLabel, taskDestinations } from "./ops.js";
import { addBlocker, adopt, askUnlink, discard, discardStale, dismissAll, dismissOne, handOff, handOffTask, instruct, setStatus, start, toParking, unlink } from "./actions.js";
import { pickFolder } from "./daemon.js";
import { MAX_ZOOM, MIN_ZOOM, clampZoom, fitZoom, percent, scrollAfterZoom, stepZoom, wheelZoomer } from "./zoom.js";
import { restore, scrollView, watchUpgrade } from "./restore.js";

const NODE_W = 240;
const NODE_H = 72;
const POP_W = 400;
// 窓が前にあるときは数秒おき、隠れているときは間隔を伸ばす。
const POLL_MS = 3000;
const POLL_HIDDEN_MS = 15000;

// 状態の語は OSC 7501 の state と Task 系ツールの status。
const STATE_LABEL = {
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
  external: "外部",
};
const REF_KIND_LABEL = { issue: "issue", pr: "PR" };
// 丸と chip の色の鍵。無い状態は既定の灰。
const STATE_TONE = {
  working: "working",
  question: "warn",
  permission: "warn",
  limit: "limit",
  error: "err",
  exited: "exited",
};
const KIND_LABEL = { task: "タスク", todo: "TodoWrite の項目", session: "セッション", ref: "参照" };

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

function stateLabel(n) {
  // 下流が全部完了した idle の peer は報告を済ませていて、こちらの返事を要さない。
  const label = n.state === "done" && state.graph.settled(n.key) ? "手が空いている (idle)" : (STATE_LABEL[n.state] ?? n.state);
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
      if (!Array.isArray(data.graph?.nodes)) throw new Error("deps の JSON に graph が無い");
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
    await fetchDeps();
    schedule();
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

// layout は辺でつながるノードを dagre で配置し、辺の無いノードはその下に格子で並べる。dagre は辺の無いノードを
// 全部 1 段目の 1 行に並べるので、タスクの無いセッションが多いと横にだけ伸びる。格子の列数は窓の幅から決める。
// depth は key → 段 (Graph.view の depth)。返すのは key → { x, y } (中心) と、全体の width、height、辺の経路を持つ
// dagre の graph。
function layout(nodes, links, depth, hostWidth) {
  const linked = new Set(links.flatMap((l) => [l.before, l.after]));
  const connected = nodes.filter((n) => linked.has(n.key));
  const isolated = nodes.filter((n) => !linked.has(n.key));

  const g = new dagre.graphlib.Graph();
  g.setGraph({ rankdir: "TB", ranker: rankByDepth(depth), nodesep: GAP_X, ranksep: GAP_Y, marginx: MARGIN_X, marginy: MARGIN_Y });
  g.setDefaultEdgeLabel(() => ({}));
  for (const n of connected) g.setNode(n.key, { width: NODE_W, height: NODE_H });
  for (const l of links) if (g.hasNode(l.before) && g.hasNode(l.after)) g.setEdge(l.before, l.after);
  dagre.layout(g);

  const pos = new Map(connected.map((n) => [n.key, g.node(n.key)]));
  let width = connected.length > 0 ? g.graph().width : 0;
  let height = connected.length > 0 ? g.graph().height : MARGIN_Y;
  if (isolated.length > 0) {
    const columns = Math.max(1, Math.floor((hostWidth - MARGIN_X * 2 + GAP_X) / (NODE_W + GAP_X)));
    const top = height + (connected.length > 0 ? GAP_Y : 0);
    isolated.forEach((n, i) => {
      const col = i % columns;
      const row = Math.floor(i / columns);
      pos.set(n.key, { x: MARGIN_X + col * (NODE_W + GAP_X) + NODE_W / 2, y: top + row * (NODE_H + GAP_Y) + NODE_H / 2 });
    });
    const rows = Math.ceil(isolated.length / columns);
    width = Math.max(width, MARGIN_X * 2 + Math.min(columns, isolated.length) * (NODE_W + GAP_X) - GAP_X);
    height = top + rows * (NODE_H + GAP_Y) - GAP_Y + MARGIN_Y;
  }
  return { g, pos, width, height };
}

function nodeCard(n, highlight) {
  const b = document.createElement("button");
  b.type = "button";
  b.className = "node";
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
  dot.className = `st ${STATE_TONE[n.state] ?? ""}`.trim();

  const lb = document.createElement("span");
  lb.className = "lb";
  const title = document.createElement("b");
  title.textContent = n.title;
  const sub = document.createElement("small");
  // 副題はセッション名 (題と違うとき)、状態、今の作業 (タスクの無いセッションと進行中の作業にだけ付く) の順。
  sub.textContent = [n.session !== n.title ? n.session : "", stateLabel(n), n.summary].filter(Boolean).join(" · ");
  if (n.summary) b.title = n.summary;
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

function edgePath(points, cls, marker, hint) {
  const p = document.createElementNS(SVG, "path");
  const d = points.map((pt, i) => `${i === 0 ? "M" : "L"} ${pt.x.toFixed(1)} ${pt.y.toFixed(1)}`).join(" ");
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
  return `${l.via.map((key) => state.graph.node(key)?.title ?? key).join(" → ")} を経由`;
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
  const scroll = { left: host.scrollLeft, top: host.scrollTop };
  host.replaceChildren();
  if (nodes.length === 0) {
    const empty = document.createElement("div");
    empty.className = "empty";
    empty.textContent = "タスクが無い";
    host.append(empty);
    return;
  }
  const { g, pos, width, height } = layout(nodes, links, depth, host.clientWidth);

  const canvas = document.createElement("div");
  canvas.className = "canvas";
  canvas.style.width = `${Math.ceil(width)}px`;
  canvas.style.height = `${Math.ceil(height)}px`;

  const svg = document.createElementNS(SVG, "svg");
  svg.setAttribute("class", "edges");
  svg.setAttribute("width", String(Math.ceil(width)));
  svg.setAttribute("height", String(Math.ceil(height)));
  const defs = document.createElementNS(SVG, "defs");
  defs.append(arrowMarker("arrow", ""), arrowMarker("arrow-chain", "chain"));
  svg.append(defs);

  const chain = chainEdges(graph);
  const drawn = links.filter((l) => g.hasEdge(l.before, l.after)).map((l) => ({ ...l, points: g.edge(l.before, l.after).points }));
  for (const l of spreadPorts(drawn, pos, NODE_W, NODE_H)) {
    const onChain = chain.has(`${l.before}\u0000${l.after}`);
    const cls = [l.kind === "task" ? "" : l.kind, onChain ? "chain" : ""].filter(Boolean).join(" ");
    const hint = l.kind === "folded" ? foldedHint(l) : (l.edge?.note ?? "");
    svg.append(edgePath(l.points, cls, onChain ? "arrow-chain" : "arrow", hint));
  }
  canvas.append(svg);

  const highlight = highlightKeys();
  for (const n of nodes) {
    const p = pos.get(n.key);
    const card = nodeCard(n, highlight);
    card.style.left = `${Math.round(p.x - NODE_W / 2)}px`;
    card.style.top = `${Math.round(p.y - NODE_H / 2)}px`;
    canvas.append(card);
  }
  host.append(canvas);
  host.scrollTo(scroll.left, scroll.top);
}

function renderToggle() {
  const done = state.graph.nodes.filter((n) => n.state === "completed").length;
  el("completed-toggle").hidden = done === 0;
  el("completed-check").checked = state.showCompleted;
  el("completed-label").textContent = `完了したタスクも表示 (${done} 件)`;
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
  el("park-title").textContent = `メモ · ${total}`;

  const filter = el("repo-filter");
  const current = state.repoFilter;
  filter.replaceChildren();
  const all = document.createElement("option");
  all.value = "";
  all.textContent = "すべての repo";
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
    none.textContent = total === 0 ? "open のメモは無い" : "この repo に open のメモは無い";
    list.append(none);
    return;
  }
  for (const row of rows) list.append(memoRow(row));
}

// ---- 単体ビュー (ポップアップ) ----

function chip(text, tone, dotTone) {
  const c = document.createElement("span");
  c.className = tone ? `chip ${tone}` : "chip";
  if (dotTone !== undefined) {
    const d = document.createElement("span");
    d.className = dotTone ? `dot ${dotTone}` : "dot";
    c.append(d);
  }
  const t = document.createElement("span");
  t.textContent = text;
  c.append(t);
  return c;
}

function linkChip(text, onClick) {
  const c = chip(text, "link");
  c.setAttribute("role", "button");
  c.tabIndex = 0;
  c.addEventListener("click", onClick);
  return c;
}

function group(label, rows) {
  const g = document.createElement("div");
  g.className = "grp";
  const ph = document.createElement("span");
  ph.className = "ph";
  ph.textContent = label;
  g.append(ph, ...rows);
  return g;
}

// nodeRow は関係するノードの 1 行: 状態の丸、題 (押すとそのノードを選ぶ)、直接か間接かの印、依存を外すボタン (あれば)。
// owner は選んでいるノード (同じセッションの blockedBy の解消を頼む宛先)。
function nodeRow(n, mark, link, owner) {
  const row = document.createElement("div");
  row.className = "lk";
  const dot = document.createElement("span");
  dot.className = `st ${STATE_TONE[n.state] ?? ""}`.trim();
  const b = document.createElement("button");
  b.type = "button";
  b.textContent = n.title;
  b.addEventListener("click", () => select({ type: "node", key: n.key }));
  const ph = document.createElement("span");
  ph.className = "ph";
  ph.textContent = mark;
  row.append(dot, b, ph);
  // edge add の note (何の待ちか) は行に添える。
  if (link?.edge?.note) {
    const note = document.createElement("span");
    note.className = "note";
    note.textContent = link.edge.note;
    row.append(note);
  }
  // `edge add` で書いた辺 (kind edge) は元の記録を消せる。同じセッションの blockedBy はそのセッションの TaskUpdate のもの。
  if (link?.kind === "edge" && link.edge) {
    const x = document.createElement("button");
    x.type = "button";
    x.className = "tb sm";
    x.textContent = "外す";
    x.disabled = state.busy;
    x.addEventListener("click", () => act(() => unlink(link)));
    row.append(x);
  }
  // 同じセッションの blockedBy は TaskUpdate のもので外せないので、そのセッションに解消を頼む。
  if (link?.kind === "task" && owner && canTell(owner)) {
    const ask = document.createElement("button");
    ask.type = "button";
    ask.className = "tb sm";
    ask.textContent = "外すよう頼む";
    ask.disabled = state.busy;
    ask.addEventListener("click", () => act(() => askUnlink(n, owner)));
    row.append(ask);
  }
  return row;
}

// related は key の直近 (直接) と、その先 (間接) のノードを並べる。direct は [ノード, 辺] の組。
function related(g, direct, all, owner) {
  const rows = [];
  const seen = new Set();
  for (const [n, link] of direct) {
    seen.add(n.key);
    rows.push(nodeRow(n, "直接", link, owner));
  }
  for (const k of all) {
    if (seen.has(k)) continue;
    const n = g.node(k);
    if (n) rows.push(nodeRow(n, "間接"));
  }
  return rows;
}

// 動詞のボタン。絵は線だけの小さな SVG。
const ICONS = {
  link: '<path d="M10 14a4 4 0 0 0 5.7 0l3-3a4 4 0 0 0-5.7-5.7l-1 1"/><path d="M14 10a4 4 0 0 0-5.7 0l-3 3a4 4 0 0 0 5.7 5.7l1-1"/>',
  memo: '<path d="M6 3h9l4 4v14H6z"/><path d="M15 3v4h4"/><path d="M9 12h6M9 16h6"/>',
  remove: '<path d="M5 12h14"/><circle cx="12" cy="12" r="9"/>',
  trash: '<path d="M4 7h16"/><path d="M9 7V4h6v3"/><path d="M6 7l1 14h10l1-14"/>',
  task: '<rect x="5" y="4" width="14" height="17" rx="2"/><path d="M9 4V2h6v2"/><path d="M9 13l2 2 4-4"/>',
  issue: '<circle cx="12" cy="12" r="9"/><path d="M12 8v5"/><path d="M12 16h.01"/>',
  folder: '<path d="M3 7h6l2 2h10v10H3z"/>',
  send: '<path d="M4 12l16-8-6 16-2-7z"/><path d="M12 13l8-9"/>',
  hand: '<path d="M4 12h12"/><path d="M12 7l5 5-5 5"/><path d="M20 5v14"/>',
  play: '<path d="M7 5l12 7-12 7z"/>',
  check: '<path d="M5 12l5 5 9-10"/>',
};

function actButton(label, icon, tone, onClick) {
  const b = document.createElement("button");
  b.type = "button";
  b.className = tone ? `act ${tone}` : "act";
  b.disabled = state.busy;
  // ICONS はコードの定数だけで、データの文字列は入れない。
  b.innerHTML = `<svg viewBox="0 0 24 24" aria-hidden="true">${ICONS[icon]}</svg>`;
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

function cancelButton() {
  const b = document.createElement("button");
  b.type = "button";
  b.className = "tb sm";
  b.textContent = "やめる";
  b.addEventListener("click", () => {
    state.picker = null;
    state.refusal = "";
    renderPopup();
  });
  return b;
}

// blockerPicker は「先に済ませるタスクを選ぶ」の一覧。
function blockerPicker(n) {
  const g = state.graph;
  const rows = candidates(g, n).map((c) => {
    const row = document.createElement("div");
    row.className = "lk";
    const dot = document.createElement("span");
    dot.className = `st ${STATE_TONE[c.state] ?? ""}`.trim();
    const b = document.createElement("button");
    b.type = "button";
    b.textContent = c.title;
    b.disabled = state.busy;
    b.addEventListener("click", () => act(() => addBlocker(c, n)));
    row.append(dot, b);
    return row;
  });
  if (rows.length === 0) {
    const none = document.createElement("span");
    none.className = "ph";
    none.textContent = "候補が無い (端点にできる、完了していないタスクが他に無い)";
    rows.push(none);
  }
  return group("先に済ませるタスクを選ぶ", [...rows, cancelButton()]);
}

// noteField は操作の文面の末尾に添える補足欄。IME の変換中の Enter は textarea なので送信にならない。
function noteField() {
  const t = document.createElement("textarea");
  t.className = "note";
  t.rows = 2;
  t.placeholder = "補足 (渡す文面の末尾に添える)";
  t.value = state.note;
  t.addEventListener("input", () => {
    state.note = t.value;
  });
  return t;
}

// refusalLine は送達が拒まれた理由。無ければ出さない。
function refusalLine() {
  const p = document.createElement("div");
  p.className = "refusal";
  p.textContent = state.refusal;
  p.hidden = state.refusal === "";
  return p;
}

function subheading(text) {
  const s = document.createElement("span");
  s.className = "ph sub";
  s.textContent = text;
  return s;
}

// destinationRows は生きているセッションの宛先の一覧。無ければその旨を 1 行出す。
function destinationRows(peers, onPick) {
  if (peers.length === 0) {
    const none = document.createElement("span");
    none.className = "ph";
    none.textContent = "生きているセッションが無い";
    return [none];
  }
  return peers.map((to) => {
    const row = document.createElement("div");
    row.className = "lk";
    const b = document.createElement("button");
    b.type = "button";
    b.textContent = destinationLabel(to);
    b.title = to.session_id;
    b.disabled = state.busy;
    b.addEventListener("click", () => onPick(to));
    row.append(b);
    return row;
  });
}

// SPAWN_SHOWN は起こす repo の候補を、「ほか n 件」を押す前に出す件数。台帳の repo は多いので畳む。
const SPAWN_SHOWN = 8;

// spawnRows は「新しいセッションを起こして渡す」の repo の一覧とフォルダの選択。first は先頭に出す repo (無ければ undefined)。
function spawnRows(first, onPick) {
  const now = Date.now();
  const all = spawnDestinations(state.data, first);
  const shown = state.moreRepos ? all : all.slice(0, SPAWN_SHOWN);
  const rows = shown.map((to) => {
    const row = document.createElement("div");
    row.className = "lk";
    const b = document.createElement("button");
    b.type = "button";
    b.textContent = spawnLabel(to, now);
    b.title = to.repo;
    b.disabled = state.busy;
    b.addEventListener("click", () => onPick(to));
    row.append(b);
    return row;
  });
  if (all.length > shown.length) {
    const more = document.createElement("button");
    more.type = "button";
    more.className = "tb sm";
    more.textContent = `ほか ${all.length - shown.length} 件…`;
    more.addEventListener("click", () => {
      state.moreRepos = true;
      renderPopup();
    });
    rows.push(more);
  }
  const pick = document.createElement("button");
  pick.type = "button";
  pick.className = "tb sm";
  pick.textContent = "フォルダを選ぶ…";
  pick.disabled = state.busy;
  pick.addEventListener("click", async () => {
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
  });
  const foot = document.createElement("div");
  foot.className = "lk";
  foot.append(pick, cancelButton());
  return [...rows, foot];
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
  const label = intent === "task" ? "タスクにするセッションを選ぶ" : "issue にするセッションを選ぶ";
  return group(label, [noteField(), refusalLine(), ...destinationRows(peers, pick), subheading("新しいセッションを起こす"), ...spawnRows({ repo: repo.repo, title: repo.title }, pick)]);
}

// taskHandPicker は「渡す」の宛先: 持ち主を除く生きているセッションと、新しく起こす repo。
function taskHandPicker(n) {
  const pick = (to) => act(() => handOffTask(n, to, state.note));
  return group("渡すセッションを選ぶ", [noteField(), refusalLine(), ...destinationRows(peerDestinations(state.data, n.session_id), pick), subheading("新しいセッションを起こす"), ...spawnRows(repoOf(n), pick)]);
}

// instructPicker は指示の文面を打って送る。textarea なので IME の変換中の Enter は送信にならず、送るのはボタンか ⌘ Enter。
function instructPicker(n) {
  const t = document.createElement("textarea");
  t.className = "note draft";
  t.rows = 4;
  t.placeholder = n.kind === "task" ? "指示 (タスクの題を添えて入力欄に打つ)" : "指示 (入力欄に打つ文面)";
  t.value = state.draft;
  const send = document.createElement("button");
  send.type = "button";
  send.className = "tb sm";
  send.textContent = "送る";
  const submit = () => {
    if (state.draft.trim() === "") return;
    act(() => instruct(n, state.draft.trim()));
  };
  send.disabled = state.busy || state.draft.trim() === "";
  send.addEventListener("click", submit);
  t.addEventListener("input", () => {
    state.draft = t.value;
    send.disabled = state.busy || state.draft.trim() === "";
  });
  t.addEventListener("keydown", (e) => {
    if (e.key === "Enter" && e.metaKey && !e.isComposing) {
      e.preventDefault();
      submit();
    }
  });
  const foot = document.createElement("div");
  foot.className = "lk";
  foot.append(send, cancelButton());
  return group(`${n.session ?? ""} へ指示を送る`, [t, refusalLine(), foot]);
}

// startPicker は着手の依頼に補足を添えて送る。
function startPicker(n) {
  const send = document.createElement("button");
  send.type = "button";
  send.className = "tb sm";
  send.textContent = "着手を頼む";
  send.disabled = state.busy;
  send.addEventListener("click", () => act(() => start(n, state.note)));
  const foot = document.createElement("div");
  foot.className = "lk";
  foot.append(send, cancelButton());
  return group(`${n.session ?? ""} に着手を頼む`, [noteField(), refusalLine(), foot]);
}

// toggle は picker を開く・閉じる動詞。
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

function stateChip(n) {
  const tone = STATE_TONE[n.state];
  const chipTone = tone === "working" ? "ok" : tone === "exited" ? "" : tone ?? "";
  return chip(stateLabel(n), chipTone, tone === "working" ? "" : tone ?? "off");
}

function nodePopup(n) {
  const g = state.graph;
  const parts = [];
  const head = document.createElement("div");
  head.className = "head";
  const ph = document.createElement("span");
  ph.className = "ph";
  ph.textContent = [KIND_LABEL[n.kind] ?? n.kind, n.task_id].filter(Boolean).join(" · ");
  const title = document.createElement("span");
  title.className = "title";
  title.textContent = n.title;
  const chips = document.createElement("div");
  chips.className = "chips";
  chips.append(stateChip(n));
  if (n.session) chips.append(chip(`${n.session} ›`, "", undefined));
  head.append(ph, title, chips);
  parts.push(head);

  // 動詞。送達を伴うもの (指示、渡す、着手、完了、削除、メモにする) は生きているセッションのタスクに、バイナリだけで済む
  // もの (メモにする = adopt、一覧から外す) は終了したセッションのタスクに出る。
  const verbs = [];
  if (canTell(n)) verbs.push(actButton("指示を送る", "send", on("instruct"), toggle("instruct")));
  if (n.endpoint && n.cwd !== undefined) verbs.push(actButton("依存を足す", "link", on("blocker"), toggle("blocker")));
  if (isOwnTask(n)) {
    verbs.push(actButton("渡す", "hand", on("hand"), toggle("hand")));
    if (n.state !== "in_progress") verbs.push(actButton("着手", "play", on("start"), toggle("start")));
    verbs.push(actButton("完了", "check", "", () => act(() => setStatus(n, "completed"))));
    verbs.push(actButton("メモにする", "memo", "", () => act(() => toParking(n))));
    verbs.push(actButton("削除", "trash", "danger", () => act(() => setStatus(n, "deleted"))));
  }
  if (canAdopt(n, state.data)) verbs.push(actButton("メモにする", "memo", "", () => act(() => adopt(n))));
  if (canDismiss(n, state.data)) verbs.push(actButton("一覧から外す", "remove", "danger", () => act(() => dismissOne(n))));
  if (verbs.length > 0) parts.push(actsBar(verbs));
  if (state.picker === "instruct") parts.push(instructPicker(n));
  if (state.picker === "blocker") parts.push(blockerPicker(n));
  if (state.picker === "hand") parts.push(taskHandPicker(n));
  if (state.picker === "start") parts.push(startPicker(n));
  if (state.refusal !== "" && !["instruct", "blocker", "hand", "start"].includes(state.picker)) parts.push(refusalLine());

  // タスクの説明と並ぶので、今の作業 (msg) は見出しを添えて分ける。
  for (const text of [n.description, n.summary ? `今の作業: ${n.summary}` : ""]) {
    if (!text) continue;
    const p = document.createElement("p");
    p.textContent = text;
    parts.push(p);
  }

  const after = g.waiters(n.key).flatMap((l) => (g.node(l.after) ? [[g.node(l.after), l]] : []));
  parts.push(group(`このタスクを待っている · ${g.reach(n.key).size}`, related(g, after, g.reach(n.key))));
  const before = g.blockers(n.key).flatMap((l) => (g.node(l.before) ? [[g.node(l.before), l]] : []));
  parts.push(group(`このタスクが待っている · ${g.upstream(n.key).size}`, related(g, before, g.upstream(n.key), n)));

  if (n.memos.length > 0) {
    parts.push(group(`扱うメモ · ${n.memos.length}`, n.memos.map((m) => memoRowInPopup(m.id, m.title, m.status, m.status === "open"))));
  }
  // 途中で置いたメモは open のものだけが repos に載る (parked の数と同じ)。
  const parked = memos().filter(({ memo }) => memo.sources.some((s) => s.task_ref === n.key));
  if (parked.length > 0) {
    parts.push(group(`途中で置いたメモ · ${parked.length}`, parked.map(({ memo }) => memoRowInPopup(memo.id, memo.title, memo.kind, true))));
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
  b.disabled = !selectable;
  b.addEventListener("click", () => select({ type: "memo", id }));
  const ph = document.createElement("span");
  ph.className = "ph";
  ph.textContent = mark;
  row.append(b, ph);
  return row;
}

function memoPopup({ memo, repo }) {
  const g = state.graph;
  const parts = [];
  const head = document.createElement("div");
  head.className = "head";
  const ph = document.createElement("span");
  ph.className = "ph";
  ph.textContent = `メモ · ${repo.title} · ${kindLabel(memo)}`;
  const title = document.createElement("span");
  title.className = "title";
  title.textContent = memo.title;
  const chips = document.createElement("div");
  chips.className = "chips";
  chips.append(chip("open", "ok", ""));
  const placedAt = memo.sources.map((s) => s.task_ref).find((k) => k && g.node(k));
  if (placedAt) chips.append(linkChip(`置いたとき: ${g.node(placedAt).title} ›`, () => select({ type: "node", key: placedAt })));
  head.append(ph, title, chips);
  parts.push(head);

  // 動詞。渡す先は生きているセッションか、新しく起こすセッション。
  parts.push(
    actsBar([
      actButton("タスクにする", "task", `pri ${on("task")}`.trim(), toggle("task")),
      actButton("issue にする", "issue", on("issue"), toggle("issue")),
      actButton("捨てる", "trash", "danger", () => act(() => discard(memo))),
    ]),
  );
  if (state.picker === "task" || state.picker === "issue") parts.push(memoHandPicker(memo, repo, state.picker));
  else if (state.refusal !== "") parts.push(refusalLine());

  if (memo.detail) {
    const p = document.createElement("p");
    p.textContent = memo.detail;
    parts.push(p);
  }
  if (memo.default_taken || memo.files?.length) {
    const dl = document.createElement("dl");
    dl.className = "kv";
    const add = (k, v) => {
      const dt = document.createElement("dt");
      dt.textContent = k;
      const dd = document.createElement("dd");
      dd.textContent = v;
      dl.append(dt, dd);
    };
    if (memo.default_taken) add("既定", memo.default_taken);
    if (memo.files?.length) add("ファイル", memo.files.join("、"));
    parts.push(dl);
  }
  const src = document.createElement("div");
  src.className = "src";
  const label = document.createElement("span");
  label.className = "ph";
  label.textContent = "置いた経緯 · 新しい順";
  src.append(label);
  for (const s of memo.sources) {
    const row = document.createElement("span");
    const em = document.createElement("em");
    em.textContent = stamp(s.at);
    const text = document.createElement("span");
    text.textContent = [s.session + (s.live ? "" : " (終了)"), s.detail].filter(Boolean).join(" · ");
    row.append(em, text);
    src.append(row);
  }
  parts.push(src);
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
    pop.setAttribute("aria-label", "タスクの詳細");
  } else if (m) {
    anchor = document.querySelector(`.mrow[data-memo="${CSS.escape(m.memo.id)}"]`);
    parts = memoPopup(m);
    side = "above";
    pop.setAttribute("aria-label", "メモの詳細");
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
  x.setAttribute("aria-label", "閉じる");
  x.textContent = "×";
  x.addEventListener("click", () => select(null));
  pop.replaceChildren(x, ...parts);
  pop.hidden = false;
  placePopup(pop, anchor, side);
}

// ---- footer ----

function relative(from) {
  const sec = Math.max(0, Math.round((Date.now() - from.getTime()) / 1000));
  if (sec < 60) return `${sec} 秒前`;
  const min = Math.round(sec / 60);
  if (min < 60) return `${min} 分前`;
  return `${Math.round(min / 60)} 時間前`;
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
    d.daemon_running ? "daemon 動作中" : "daemon 止まっている",
    state.readAt ? `${relative(state.readAt)}に読み直し` : "",
    "·",
    `止まっている ${count((n) => n.stuck)} · 作業中 ${count((n) => n.state === "working")} · 待ち ${count((n) => n.state === "done" || n.state === "unknown")} · 完了 ${count((n) => n.state === "completed")}${state.showCompleted ? "" : " (非表示)"}`,
    "·",
    `タスク ${count((n) => n.kind === "task")} · メモ ${memoCount} · セッション ${sessions}`,
  ];
  if (state.error) parts.push("·", `読み直しに失敗: ${state.error}`);
  dot.className = d.daemon_running ? "dot" : "dot off";
  text.className = state.error ? "err-text" : "";
  // 操作の結果はしばらく footer を占め、その後に件数へ戻る。
  const noticed = state.notice !== "" && Date.now() < state.noticeUntil;
  text.textContent = noticed ? state.notice : parts.filter(Boolean).join(" ");
  if (noticed) text.className = "notice";
}

// renderBulk はまとめて行う操作のボタン: 終了したセッションのタスクを外す、古いメモを捨てる。該当があるときだけ出す。
function renderBulk() {
  const ended = visibleNodes().filter((n) => canDismiss(n, state.data));
  const dismissBtn = el("dismiss-ended");
  dismissBtn.hidden = ended.length === 0;
  dismissBtn.textContent = `終了したセッションのタスク ${ended.length} 件を外す`;
  dismissBtn.disabled = state.busy;
  dismissBtn.onclick = () => act(() => dismissAll(ended));

  const days = state.data.stale_memo_days;
  const stale = state.data.repos.flatMap((r) => r.memos).filter((m) => isStale(m, Date.now(), days)).length;
  const staleBtn = el("discard-stale");
  staleBtn.hidden = stale === 0;
  staleBtn.textContent = `${days} 日より古いメモ ${stale} 件を捨てる`;
  staleBtn.disabled = state.busy;
  staleBtn.onclick = () => act(() => discardStale(days));
}

// ---- 全体 ----

function render() {
  if (state.data === null) {
    el("empty").textContent = state.error ? `依存図を読めない: ${state.error}` : "依存図を読んでいる…";
    renderFoot();
    return;
  }
  renderGraph();
  applyZoom();
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
// 描画には触れず、描いた canvas を枠で包んで scale とスクロール量だけを変える。拡大率は図を読み直しても保つ
// (勝手に全体表示へ戻さない)。

let zoom = 1;

// applyZoom は renderGraph が作った canvas を枠で包み、canvas に scale を、枠に拡大率を掛けた大きさを当てる。
// 枠は canvas の作り直しで消えるので、描くたびに呼ぶ。
function applyZoom() {
  const canvas = el("graph").querySelector(".canvas");
  if (canvas === null) return;
  let frame = canvas.parentElement;
  if (!frame.classList.contains("zoom-frame")) {
    frame = document.createElement("div");
    frame.className = "zoom-frame";
    canvas.replaceWith(frame);
    frame.append(canvas);
  }
  frame.style.width = `${Math.ceil(parseFloat(canvas.style.width) * zoom)}px`;
  frame.style.height = `${Math.ceil(parseFloat(canvas.style.height) * zoom)}px`;
  canvas.style.transform = zoom === 1 ? "" : `scale(${zoom})`;
}

// setZoom は拡大率を next にし、pointer (#graph の左上からの距離) が指す図の点を同じ画面位置に残す。
// pointer が無ければ見えている領域の中央を残す。
function setZoom(next, pointer) {
  const host = el("graph");
  const to = clampZoom(next);
  const p = pointer ?? { x: host.clientWidth / 2, y: host.clientHeight / 2 };
  const left = scrollAfterZoom(host.scrollLeft, p.x, zoom, to);
  const top = scrollAfterZoom(host.scrollTop, p.y, zoom, to);
  zoom = to;
  applyZoom();
  host.scrollTo(left, top);
  renderZoom();
  if (state.selected !== null) renderPopup();
}

// fitAll は図全体が見える領域に収まる拡大率にし、左上から見せる。下限で止まった分はスクロールで見る。
function fitAll() {
  const host = el("graph");
  const canvas = host.querySelector(".canvas");
  if (canvas === null) return;
  setZoom(fitZoom(parseFloat(canvas.style.width), parseFloat(canvas.style.height), host.clientWidth, host.clientHeight));
  host.scrollTo(0, 0);
}

function renderZoom() {
  el("zoom-reset").textContent = percent(zoom);
  el("zoom-in").disabled = zoom >= MAX_ZOOM;
  el("zoom-out").disabled = zoom <= MIN_ZOOM;
}

function pointerIn(e) {
  const r = el("graph").getBoundingClientRect();
  return { x: e.clientX - r.left, y: e.clientY - r.top };
}

el("zoom-in").addEventListener("click", () => setZoom(stepZoom(zoom, 1)));
el("zoom-out").addEventListener("click", () => setZoom(stepZoom(zoom, -1)));
el("zoom-reset").addEventListener("click", () => setZoom(1));
el("zoom-fit").addEventListener("click", fitAll);
// Cmd + ホイール (と Chromium 系のピンチが送る ctrl + ホイール)。passive にすると preventDefault が効かずスクロールされる。
const wheelZoom = wheelZoomer();
el("graph").addEventListener("wheel", (e) => {
  const next = wheelZoom({ deltaY: e.deltaY, modifier: e.metaKey || e.ctrlKey, t: e.timeStamp }, zoom);
  if (next === null) return;
  e.preventDefault();
  setZoom(next, pointerIn(e));
}, { passive: false });
// WKWebView のトラックパッドのピンチは gesture* で届き、scale は gesturestart からの倍率。
let gestureFrom = 1;
el("graph").addEventListener("gesturestart", (e) => {
  e.preventDefault();
  gestureFrom = zoom;
});
el("graph").addEventListener("gesturechange", (e) => {
  e.preventDefault();
  setZoom(gestureFrom * e.scale, pointerIn(e));
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
el("park-list").addEventListener("click", () => select(null));
el("pop").addEventListener("click", (e) => e.stopPropagation());
document.addEventListener("keydown", (e) => {
  if (e.key === "Escape" && state.selected !== null) select(null);
});
// スクロールと窓の大きさの変化でポップアップの位置を追わせる。
for (const target of [el("graph"), el("park-list")]) target.addEventListener("scroll", () => { if (state.selected !== null) renderPopup(); });
window.addEventListener("resize", () => { if (state.selected !== null) renderPopup(); });
document.addEventListener("visibilitychange", () => {
  if (!document.hidden) fetchDeps();
});

// 新しいバージョンへ切り替えるときに引き継ぐ画面の状態 (restore.js)。
const views = {
  selected: { get: () => state.selected, set: (v) => { state.selected = v; } },
  showCompleted: { get: () => state.showCompleted, set: (v) => { state.showCompleted = v === true; } },
  repoFilter: { get: () => state.repoFilter, set: (v) => { state.repoFilter = String(v); } },
  // 開いていた選択と、打ちかけの指示と補足。
  picker: { get: () => state.picker, set: (v) => { state.picker = typeof v === "string" ? v : null; } },
  draft: { get: () => state.draft, set: (v) => { state.draft = String(v); } },
  note: { get: () => state.note, set: (v) => { state.note = String(v); } },
  zoom: { get: () => zoom, set: (v) => { zoom = clampZoom(Number(v) || 1); renderZoom(); } },
  graphScroll: scrollView(el("graph")),
  parkScroll: scrollView(el("park-list")),
};

await fetchDeps();
await restore(views);
render();
schedule();
watchUpgrade(views, el("upgrade"));
