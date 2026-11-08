// app.js を happy-dom の DOM で走らせ、fixtures/deps.json の図を描いた結果を DOM で確かめる。描いた画面は
// SNAPSHOT_DIR (既定は repo の tmp/gui-snapshots) へスナップショットの HTML (DOM と style.css を 1 枚にしたもの、
// script は外す) として書き出す。画素では判定せず、見た目の差はスナップショットをブラウザで開いて見る。
//
// ブラウザを起動しないのは、Claude Code の sandbox の中では Chromium・WebKit・Firefox のどれも Mach IPC の拒否で
// 落ちるため。daemon の代わりは window.wails の差し替えで行い、Call.ByName が fixture を返す。
// 配置は dagre が JS で数えて style の left / top に書くので、レイアウトの無い DOM でも位置を確かめられる。
// レイアウトに頼る値 (#graph の clientWidth / clientHeight) だけはここで固定する。
import { after, before, mock, test } from "node:test";
import assert from "node:assert/strict";
import { mkdir, readFile, writeFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { GlobalRegistrator } from "@happy-dom/global-registrator";
import { STATE_TONE } from "../src/card.js";

const here = path.dirname(fileURLToPath(import.meta.url));
const SRC = path.join(here, "..", "src");
const FIXTURE = path.join(here, "fixtures", "deps.json");
const SHOTS = path.resolve(process.env.SNAPSHOT_DIR || path.join(here, "..", "..", "..", "tmp", "gui-snapshots"));
// 窓 1280 × 800 から header・右の列・凡例・メモの一覧・footer を除いた #graph の大きさ。
const HOST = { width: 1000, height: 520 };
// メモの古さ (stale_memo_days) を数える現在時刻。
const NOW = new Date("2026-11-07T12:00:00+09:00");

let css;
// answers は window.wails の Call.ByName が返す値。fixtureDeps は fixture の deps の文字列。
const answers = {};
let fixtureDeps;
// main.Daemon.Open に渡された target。openFailure が空でなければ呼び出しをその理由で失敗させる。
const opened = [];
let openFailure = "";

// setHost は #graph の見える領域の大きさを決める。「全体を表示」の拡大率がこれで決まる。
function setHost({ width, height }) {
  const host = document.getElementById("graph");
  Object.defineProperty(host, "clientWidth", { configurable: true, value: width });
  Object.defineProperty(host, "clientHeight", { configurable: true, value: height });
}

// resize は #graph の見える領域を size にし、窓の大きさが変わったことを知らせる。
function resize(size) {
  setHost(size);
  window.dispatchEvent(new Event("resize"));
}

before(async () => {
  const [html, deps] = await Promise.all([readFile(path.join(SRC, "index.html"), "utf8"), readFile(FIXTURE, "utf8"), mkdir(SHOTS, { recursive: true })]);
  fixtureDeps = deps;
  css = await readFile(path.join(SRC, "style.css"), "utf8");
  mock.timers.enable({ apis: ["Date"], now: NOW });
  GlobalRegistrator.register({ url: "http://agent-tasks.test/", width: 1280, height: 800 });
  document.body.innerHTML = html.match(/<body>([\s\S]*)<\/body>/)[1];
  setHost(HOST);
  Object.assign(answers, { "main.Daemon.Deps": deps, "main.Daemon.Restore": "", "main.Daemon.PickFolder": "" });
  window.wails = {
    Call: {
      ByName: async (name, ...args) => {
        if (name === "main.Daemon.Open") {
          opened.push(args[0]);
          if (openFailure !== "") throw new Error(openFailure);
          return "";
        }
        if (!(name in answers)) throw new Error(`fixture に無い呼び出し: ${name}`);
        return answers[name];
      },
    },
    Events: { On: () => {} },
  };
  // app.js は import の中で最初の描画まで済ませる (top-level await)。
  await import("../src/app.js");
});

after(async () => {
  // app.js の読み直しの timer は window ごと止める。
  await GlobalRegistrator.unregister();
  mock.timers.reset();
});

// snapshot は今の DOM を、script を外して style.css を埋め込んだ 1 枚の HTML として name.html に書く。
async function snapshot(name) {
  const doc = document.documentElement.cloneNode(true);
  doc.setAttribute("lang", "ja");
  for (const s of doc.querySelectorAll("script")) s.remove();
  const head = doc.querySelector("head") ?? doc.insertBefore(document.createElement("head"), doc.firstChild);
  for (const l of head.querySelectorAll('link[rel="stylesheet"]')) l.remove();
  head.insertAdjacentHTML("afterbegin", '<meta charset="utf-8"><title>agent-tasks</title>');
  const style = document.createElement("style");
  style.textContent = css;
  head.append(style);
  await writeFile(path.join(SHOTS, `${name}.html`), `<!doctype html>\n${doc.outerHTML}\n`);
}

// cards は host (依存図の #graph か右の列の #column、既定は両方) に描いた箱を key → { classes, left, top } で返す。
// 右の列の箱は CSS の flex で並ぶので left / top を持たない (NaN)。
function cards(host = "#graph, #column") {
  const list = [...document.querySelectorAll(`:is(${host}) .node`)].map((e) => ({
    key: e.dataset.key,
    classes: [...e.classList],
    left: parseFloat(e.style.left),
    top: parseFloat(e.style.top),
  }));
  return new Map(list.map((c) => [c.key, c]));
}

const keysWith = (byKey, cls) => [...byKey.values()].filter((c) => c.classes.includes(cls)).map((c) => c.key).sort();
const q = (selector) => document.querySelector(selector);
const count = (selector) => document.querySelectorAll(selector).length;
// 矢印の付いた辺の path。ホバーの当たり (path.hit) は矢印を持たないので数えない。
const EDGE = "svg.edges path[marker-end]";

test("完了を隠した既定の図: 完了していないノードと、完了していないノードへ続く完了を除いた箱を描く", () => {
  const byKey = cards();
  assert.deepEqual([...byKey.keys()].sort(), [
    "pr:usadamasa/agents-daemon#254",
    "sid:gamma",
    "sid:zeta",
    "task:alpha/2",
    "task:alpha/4",
    "task:alpha/5",
    "task:beta/1",
    "task:beta/2",
    "task:beta/3",
  ]);
  assert.deepEqual(keysWith(byKey, "halt"), ["pr:usadamasa/agents-daemon#254", "task:beta/1"]);
  assert.deepEqual(keysWith(byKey, "gone"), ["sid:gamma"]);
});

test("完了を隠した既定の図: 状態の丸に状態ごとの色の class を付ける", () => {
  const dot = (key) => q(`.node[data-key="${key}"] .st`).getAttribute("class");
  assert.equal(dot("task:alpha/2"), "st working");
  assert.equal(dot("task:beta/1"), "st warn");
  assert.equal(dot("sid:gamma"), "st exited");
  assert.equal(dot("task:beta/2"), "st");
});

test("完了を隠した既定の図: マージせずに閉じた PR は完了で畳まず、「取りやめ」と手当ての要る強調で描く", () => {
  const card = q('.node[data-key="pr:usadamasa/agents-daemon#254"]');
  assert.ok(["attn", "drop", "halt"].every((c) => card.classList.contains(c)), card.className);
  // 題は daemon が引いた PR の題で、番号と repo は副題に残す (#472)。
  assert.equal(card.querySelector("b").textContent, "依存図の色を状態ごとに分ける");
  assert.equal(card.querySelector("small").textContent, "取りやめ · PR #254 · usadamasa/agents-daemon");
  assert.equal(card.querySelector(".st").getAttribute("class"), "st drop");
});

test("完了を隠した既定の図: 見える辺と隠した完了を通り抜けた辺を描き、longest chain の辺に印を付ける", () => {
  assert.equal(count(EDGE), 5);
  assert.equal(count(`${EDGE}.chain`), 2);
  assert.equal(count(`${EDGE}.folded`), 1);
  assert.equal(count(`${EDGE}.edge`), 2);
  const hints = [...document.querySelectorAll("svg.edges g.hint title")].map((t) => t.textContent);
  assert.deepEqual(hints.sort(), ["API の形が決まるのを待つ", "PR #254 のマージを待つ", "テストを直す を経由"]);
});

// groups は図の領域のセッションの枠を、見出しとカードの key で返す。
function groups() {
  return [...document.querySelectorAll("#graph .group")].map((g) => ({
    label: g.querySelector("h2").textContent,
    keys: [...g.querySelectorAll(".node")].map((e) => e.dataset.key),
  }));
}

test("完了を隠した既定の図: 辺でつながるノードを段の浅い順に上から描き、辺の無い動いているノードをセッションの枠に、終わったノードを右の列に並べる", () => {
  const graph = cards("#graph");
  assert.deepEqual([...graph.keys()].sort(), [
    "pr:usadamasa/agents-daemon#254",
    "sid:zeta",
    "task:alpha/2",
    "task:alpha/4",
    "task:alpha/5",
    "task:beta/1",
    "task:beta/2",
    "task:beta/3",
  ]);
  const top = (key) => graph.get(key).top;
  assert.ok(top("task:alpha/2") < top("task:beta/1"));
  assert.equal(top("task:beta/1"), top("task:alpha/4"));
  assert.ok(top("task:beta/1") < top("task:beta/2"));
  assert.ok(top("task:beta/2") < top("task:beta/3"));
  assert.equal(q("#column").hidden, false);
  assert.deepEqual([...cards("#column").keys()], ["sid:gamma"]);
  assert.equal(q("#column h2").textContent, "終わった作業 · 1");
  assert.deepEqual(groups(), [{ label: "zeta", keys: ["sid:zeta"] }]);
  // 枠は線のあるかたまりの下に置く。
  const lowest = Math.max(...[...graph.values()].filter((c) => !Number.isNaN(c.top)).map((c) => c.top));
  assert.ok(parseFloat(q("#graph .group").style.top) >= lowest + 72);
});

test("完了を隠した既定の図: 枠の中のカードはセッション名を繰り返さず、題がセッション名なら今の作業を題にする", () => {
  const card = q('#graph .group .node[data-key="sid:zeta"]');
  assert.equal(card.querySelector("b").textContent, "issue を読んでいる");
  assert.equal(card.querySelector("small").textContent, "作業中");
});

// loadDeps はアプリに deps を読み直させる。窓が前に出たときの読み直し (visibilitychange) を使う。
async function loadDeps(text) {
  answers["main.Daemon.Deps"] = text;
  document.dispatchEvent(new Event("visibilitychange"));
  await new Promise((resolve) => setTimeout(resolve, 0));
}

// withoutLinks は fixture から線を全部除いた deps。
function withoutLinks() {
  const data = JSON.parse(fixtureDeps);
  data.graph.links = [];
  data.graph.folded.links = [];
  return data;
}

test("線が 1 本も無い図: 動いているカードをセッションの枠に並べ、全体表示でも 100% のまま縮めない", async () => {
  const narrow = { width: 600, height: 200 };
  setHost(narrow);
  await loadDeps(JSON.stringify(withoutLinks()));
  try {
    assert.equal(count(EDGE), 0);
    assert.equal(count("#graph .empty"), 0);
    assert.deepEqual(groups().map((g) => g.label).sort(), ["alpha", "beta", "zeta", "参照"]);
    assert.deepEqual(groups().find((g) => g.label === "参照").keys, ["pr:usadamasa/agents-daemon#254"]);
    assert.deepEqual([...cards("#column").keys()], ["sid:gamma"]);
    assert.equal(q("#legend").hidden, false);
    const frame = q("#graph .zoom-frame");
    assert.ok(parseFloat(frame.style.height) > narrow.height, "枠の高さが見える領域を超える");
    assert.equal(q("#zoom-reset").textContent, "100%");
    assert.equal(q("#zoom-fit").getAttribute("aria-pressed"), "true");
    assert.equal(q("#graph .canvas").style.transform, "");
    await snapshot("graph-no-links");
  } finally {
    setHost(HOST);
    await loadDeps(fixtureDeps);
  }
});

test("線が 1 本も無い図: 窓の幅に合わせて枠を折り返し直す", async () => {
  await loadDeps(JSON.stringify(withoutLinks()));
  const rows = () => new Set([...document.querySelectorAll("#graph .group")].map((g) => g.style.top)).size;
  try {
    assert.ok(rows() < 4, `広い窓の行数 ${rows()}`);
    resize({ width: 400, height: 320 });
    assert.equal(rows(), 4);
  } finally {
    resize(HOST);
    await loadDeps(fixtureDeps);
  }
});

test("動いているカードが無い図: 図の領域にその旨を出し、終わったカードは右の列に残す", async () => {
  const data = JSON.parse(fixtureDeps);
  const onlyEnded = (n) => n.key === "sid:gamma";
  data.graph.nodes = data.graph.nodes.filter(onlyEnded);
  data.graph.links = [];
  data.graph.folded.nodes = data.graph.folded.nodes.filter(onlyEnded);
  data.graph.folded.links = [];
  await loadDeps(JSON.stringify(data));
  try {
    assert.equal(q("#graph .empty").textContent, "動いている作業は無い");
    assert.deepEqual([...cards("#column").keys()], ["sid:gamma"]);
  } finally {
    await loadDeps(fixtureDeps);
  }
});

test("完了を隠した既定の図: 止まったノードに下流の数を、メモを置いたノードにメモの数を添える", () => {
  assert.equal(q('.node[data-key="task:beta/1"] .bd').textContent, "↓2 ◇1");
  assert.equal(q('.node[data-key="task:alpha/2"] .bd').textContent, "◇1");
});

test("完了を隠した既定の図: メモの一覧と凡例と footer を出す", async () => {
  assert.equal(count("#park-list .mrow"), 2);
  assert.equal(q("#park-title").textContent, "メモ · 2");
  assert.equal(q("#legend").hidden, false);
  assert.equal(q("#completed-label").textContent, "完了したタスクも表示 (2 件)");
  assert.equal(q("#discard-stale").hidden, true);
  assert.match(q("#foot-text").textContent, /daemon 動作中 .*止まっている 2 · 作業中 2 · 待ち 0 · 完了 2 \(非表示\)/);
  // 右の列は header と footer の間の全高を取り、凡例とメモの一覧は依存図の列の幅だけを取る。
  // 要素どうしを assert.equal で比べると、食い違ったときに happy-dom の要素の inspect が終わらないので真偽で比べる。
  const stage = q(".stage");
  assert.ok(q("#column").parentElement === stage, "#column は .stage の直下");
  for (const id of ["#graph", "#legend", "#park"]) {
    const box = q(id).parentElement;
    assert.ok(box !== stage && box.parentElement === stage, `${id} は .stage の中の左の列`);
    assert.ok(box === q("#graph").parentElement, `${id} は #graph と同じ列`);
  }
  await snapshot("graph");
});

test("header: 見せ方は依存図だけなので、見せ方の切り替えのボタンを出さない", () => {
  const labels = [...document.querySelectorAll("header.bar button")].map((b) => b.textContent);
  assert.ok(!labels.includes("依存図") && !labels.includes("一覧"), labels.join(" / "));
  assert.equal(count('header.bar [aria-label="見せ方"]'), 0);
});

test("ノードを選ぶと単体ビューを開き、待っている先と待たれている先を並べる", async () => {
  q('.node[data-key="task:beta/1"]').click();
  const pop = q("#pop");
  assert.equal(pop.hidden, false);
  assert.equal(pop.querySelector(".title").textContent, "画面を描く");
  const groups = [...pop.querySelectorAll(".grp > .ph")].map((p) => p.textContent);
  assert.ok(groups.includes("このタスクを待っている · 2"), groups.join(" / "));
  assert.ok(groups.includes("このタスクが待っている · 2"), groups.join(" / "));
  // 題が切れても見分けられるよう、関係の行の題は全文を tooltip に持つ。
  const rows = [...pop.querySelectorAll(".grp .lk > button:first-of-type")];
  assert.ok(rows.length > 0);
  for (const b of rows) assert.equal(b.title, b.textContent);
  await snapshot("graph-popup");
  document.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape" }));
  assert.equal(pop.hidden, true);
});

const flush = () => new Promise((resolve) => setTimeout(resolve, 0));
const escape = () => document.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape" }));

test("タスクの説明: URL、#123、相対パスをリンクにし、押すと target を開いて単体ビューは閉じない", async () => {
  q('.node[data-key="task:beta/2"]').click();
  const pop = q("#pop");
  const as = [...pop.querySelectorAll("p a.ref")];
  assert.deepEqual(as.map((a) => a.textContent), ["https://example.com/a", "#12", "gui/main.go:5"]);
  assert.equal(pop.querySelector("p").textContent, "https://example.com/a と #12 と gui/main.go:5 を見る");
  opened.length = 0;
  for (const a of as) a.click();
  await flush();
  assert.deepEqual(opened, ["https://example.com/a", "https://github.com/usadamasa/agents-daemon/issues/12", "/repo/agents-daemon/gui/main.go"]);
  assert.equal(pop.hidden, false);
  await snapshot("graph-popup-links");
  escape();
});

test("リンクを開けなかったら理由を footer に出す", async () => {
  q('.node[data-key="task:beta/2"]').click();
  openFailure = "開けない: /repo/agents-daemon/gui/main.go";
  try {
    q("#pop p a.ref").click();
    await flush();
    assert.ok(q("#foot-text").textContent.includes(openFailure), q("#foot-text").textContent);
  } finally {
    openFailure = "";
    escape();
  }
});

test("メモの detail: repo を基準にリンクにする", async () => {
  q('.mrow[data-memo="memo-2"]').click();
  const as = [...q("#pop").querySelectorAll("p a.ref")];
  opened.length = 0;
  for (const a of as) a.click();
  await flush();
  assert.deepEqual(opened, ["/repo/agents-daemon/gui/main.go", "https://github.com/usadamasa/agents-daemon/issues/7"]);
  escape();
});

test("辺の note: 辺の待つ側の repo を基準にリンクにする", async () => {
  q('.node[data-key="pr:usadamasa/agents-daemon#254"]').click();
  const a = q("#pop .lk .note a.ref");
  assert.equal(a.textContent, "#254");
  opened.length = 0;
  a.click();
  await flush();
  assert.deepEqual(opened, ["https://github.com/usadamasa/agents-daemon/issues/254"]);
  escape();
});

test("PR のノードの単体ビュー: 見出しは PR の題で、番号と repo は状態の chip に残す", async () => {
  q('.node[data-key="pr:usadamasa/agents-daemon#254"]').click();
  const pop = q("#pop");
  assert.equal(pop.querySelector(".title").textContent, "依存図の色を状態ごとに分ける");
  assert.equal(pop.querySelector(".chips .chip").textContent, "取りやめ · PR #254 · usadamasa/agents-daemon");
  // 関係の見出しの主語は開いたノードの種類に合わせる。
  const groups = [...pop.querySelectorAll(".grp > .ph")].map((p) => p.textContent);
  assert.ok(groups.some((t) => t.startsWith("この PR を待っている · ")), groups.join(" / "));
  assert.ok(groups.some((t) => t.startsWith("この PR が待っている · ")), groups.join(" / "));
  await snapshot("graph-popup-ref");
  document.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape" }));
});

test("状態の丸の色は .dot と .st で 1 つの規則を共有し、色の鍵の無い丸は灰で出る", () => {
  for (const tone of new Set(Object.values(STATE_TONE))) {
    assert.match(css, new RegExp(`^:is\\(\\.dot, \\.st\\)\\.${tone} \\{ background: `, "m"), tone);
  }
  assert.doesNotMatch(css, /^\.(dot|st)\.[\w-]+ \{ background: /m);
  assert.match(css, /^:is\(\.dot, \.st\) \{[^}]*background: var\(--gray-dot\);/m);
  assert.doesNotMatch(css, /^\.(dot|st) \{[^}]*background: /m);
});

// スクロールの跳ね返りは WKWebView の描画でしか起きず、happy-dom では再現できないので規則の有無で確かめる。
test("窓とスクロールする領域はどれも、端を越えたスワイプで跳ね返らない", () => {
  assert.match(css, /^\* \{ overscroll-behavior: none; \}/m);
  assert.doesNotMatch(css, /overscroll-behavior: (auto|contain)/);
});

test("単体ビューの状態の丸は、状態の色の鍵を class に持つ", () => {
  const chipDot = (key) => {
    q(`.node[data-key="${key}"]`).click();
    const cls = q("#pop .chips .chip .dot").getAttribute("class");
    document.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape" }));
    return cls;
  };
  assert.equal(chipDot("task:alpha/2"), "dot working");
  assert.equal(chipDot("sid:gamma"), "dot exited");
  assert.equal(chipDot("task:beta/2"), "dot");
});

test("緑の丸は状態の丸と同じ working の鍵で描く: daemon が動いている footer と open のメモ", () => {
  assert.equal(q("#foot .dot").getAttribute("class"), "dot working");
  q(".mrow").click();
  assert.equal(q("#pop .chips .chip .dot").getAttribute("class"), "dot working");
  document.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape" }));
});

test("作業のノード: 題そのものを押すとその場で直せ、自動で付けた題は chip で分かり、終了したものは一覧から外せる", async () => {
  // 箱の副題には出どころを出さない (そこでは何もできない)。
  assert.doesNotMatch(q('.node[data-key="sid:zeta"] small').textContent, /題/);
  q('.node[data-key="sid:zeta"]').click();
  const pop = q("#pop");
  // 自動で付けた題の chip は出どころの説明で、状態の色 (ok / warn / limit / err) を使わない。
  assert.equal(pop.querySelector(".chips .chip.guess").textContent, "会話から自動で付けた題");
  assert.equal(pop.querySelectorAll(".chips .chip.warn").length, 0);
  // 動詞のバーに「題を直す」は無く、題 (対象) そのものが入口。
  let verbs = [...pop.querySelectorAll(".acts .act")].map((b) => b.textContent);
  assert.ok(!verbs.includes("題を直す") && !verbs.includes("一覧から外す"), verbs.join(" / "));
  const heads = [...pop.querySelectorAll(".grp > .ph")].map((p) => p.textContent);
  assert.ok(heads.some((t) => t.startsWith("このセッションを待っている · ")), heads.join(" / "));
  const title = pop.querySelector(".title");
  assert.equal(title.getAttribute("role"), "button");
  title.click();
  const input = pop.querySelector('.title input[type="text"]');
  assert.equal(input.value, "zeta");
  await snapshot("graph-popup-work");
  input.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
  assert.equal(pop.querySelector(".title").textContent, "zeta");
  document.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape" }));

  q('.node[data-key="sid:gamma"]').click();
  verbs = [...pop.querySelectorAll(".acts .act")].map((b) => b.textContent);
  assert.ok(verbs.includes("一覧から外す") && verbs.includes("メモにする"), verbs.join(" / "));
  assert.equal(pop.querySelector(".title").getAttribute("role"), "button");
  // prompt から付けた題には chip を出さない。
  assert.equal(pop.querySelector(".chips .chip.guess"), null);
  // タスクのノードの題は直せない。
  document.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape" }));
  q('.node[data-key="task:beta/1"]').click();
  assert.equal(pop.querySelector(".title").getAttribute("role"), null);
  assert.equal(q("#dismiss-ended").textContent, "終了したセッションのタスクと作業 1 件を外す");
  document.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape" }));
});

test("完了も表示した図: 完了したノードも描き、通り抜けていた辺を元の辺で描く", async () => {
  q("#completed-check").click();
  try {
    const byKey = cards();
    assert.equal(byKey.size, 11);
    assert.deepEqual(keysWith(byKey, "gone"), ["sid:gamma", "task:alpha/1", "task:alpha/3"]);
    assert.equal(count(EDGE), 7);
    assert.equal(count(`${EDGE}.folded`), 0);
    assert.equal(cards("#graph").size, 10);
    assert.ok(byKey.get("task:alpha/1").top < byKey.get("task:alpha/2").top);
    assert.ok(byKey.get("task:alpha/3").top < byKey.get("task:alpha/4").top);
    await snapshot("graph-completed");
  } finally {
    q("#completed-check").click();
  }
  assert.equal(cards().size, 9);
});

// view は拡大縮小の今の見え方: 拡大率の表示、全体表示のオンオフ、枠の大きさと中心の余白、スクロール量。
function view() {
  const frame = q("#graph .zoom-frame");
  const host = q("#graph");
  return {
    label: q("#zoom-reset").textContent,
    fit: q("#zoom-fit").getAttribute("aria-pressed"),
    width: parseFloat(frame.style.width),
    height: parseFloat(frame.style.height),
    marginLeft: parseFloat(frame.style.marginLeft || "0"),
    marginTop: parseFloat(frame.style.marginTop || "0"),
    scrollLeft: host.scrollLeft,
    scrollTop: host.scrollTop,
  };
}

// assertFitted は枠が size に収まり、余った分を左右と上下に等しく空けて中心に置いていることを確かめる。
function assertFitted(v, size) {
  assert.equal(v.fit, "true");
  assert.ok(v.width <= size.width, `枠の幅 ${v.width}`);
  assert.ok(v.height <= size.height, `枠の高さ ${v.height}`);
  assert.equal(v.marginLeft, (size.width - v.width) / 2);
  assert.equal(v.marginTop, (size.height - v.height) / 2);
  assert.ok(v.marginLeft > 0 || v.marginTop > 0, "どちらかの向きに余白が残る");
}

test("既定の図: 全体を表示はオンで、図を見える領域に収めて中心に置く", () => {
  const v = view();
  assertFitted(v, HOST);
  assert.ok(q("#zoom-fit").classList.contains("on"));
  assert.ok(parseInt(v.label, 10) <= 100, `拡大率が ${v.label}`);
});

test("縮小した図: 窓を小さくすると全体表示が 100% より小さくし、図全体を見える領域の中心に収める", async () => {
  const small = { width: 480, height: 320 };
  resize(small);
  try {
    const v = view();
    assert.ok(parseInt(v.label, 10) < 100, `拡大率が ${v.label}`);
    assertFitted(v, small);
    assert.match(q("#graph .canvas").style.transform, /^scale\(0\.\d+\)$/);
    await snapshot("graph-fit");
  } finally {
    resize(HOST);
  }
  assertFitted(view(), HOST);
});

test("下限より小さくしないと収まらない図: 下限で止め、上 (根の側) と横の中心を見せる", async () => {
  const tiny = { width: 120, height: 90 };
  resize(tiny);
  try {
    const v = view();
    assert.equal(v.label, "30%");
    assert.equal(v.fit, "true");
    assert.ok(v.width > tiny.width && v.height > tiny.height, `枠 ${v.width} × ${v.height}`);
    assert.equal(v.marginLeft, 0);
    assert.equal(v.marginTop, 0);
    assert.equal(v.scrollTop, 0);
    assert.equal(v.scrollLeft, (v.width - tiny.width) / 2);
    // 拡大率の変わらない描き直しでは、利用者が動かしたスクロールを保つ。
    q("#graph").scrollTo(5, 40);
    q("#completed-check").click();
    q("#completed-check").click();
    assert.deepEqual([q("#graph").scrollLeft, q("#graph").scrollTop], [5, 40]);
    await snapshot("graph-fit-min");
  } finally {
    resize(HOST);
  }
});

test("全体表示がオンの間は、描き直すたびに合わせ直し、同じ図なら拡大率も位置も変えない", () => {
  const first = view();
  q("#completed-check").click();
  try {
    // 完了も表示すると図が変わり、その図に合わせ直す。
    assertFitted(view(), HOST);
  } finally {
    q("#completed-check").click();
  }
  assert.deepEqual(view(), first);
});

test("利用者が拡大縮小すると全体表示をオフにし、描き直しても拡大率を保つ。押し直すとまた合わせる", async () => {
  const fitted = view();
  q("#zoom-in").click();
  try {
    const zoomed = view();
    assert.equal(zoomed.fit, "false");
    assert.ok(!q("#zoom-fit").classList.contains("on"));
    assert.notEqual(zoomed.label, fitted.label);
    q("#completed-check").click();
    q("#completed-check").click();
    resize(HOST);
    assert.equal(view().label, zoomed.label);
    assert.equal(view().fit, "false");
    q("#zoom-reset").click();
    assert.equal(view().label, "100%");
    assert.equal(view().fit, "false");
    await snapshot("graph-fit-off");
  } finally {
    q("#zoom-fit").click();
  }
  assert.deepEqual(view(), fitted);
  // オンのときに押すとオフになり、拡大率はそのまま残す。
  q("#zoom-fit").click();
  try {
    assert.equal(view().fit, "false");
    assert.equal(view().label, fitted.label);
  } finally {
    q("#zoom-fit").click();
  }
  assert.equal(view().fit, "true");
});
