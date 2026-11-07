// index.html を Chromium で開き、fixtures/deps.json の図を描いた結果を DOM で確かめる。同じ図のスクリーンショットを
// SCREENSHOT_DIR (既定は repo の tmp/gui-screenshots) へ出す。画素では判定せず、見た目の差は PR のレビューで見る。
//
// daemon の代わりは window.wails の差し替えで行う。Wails の asset server が出す /wails/runtime.js は空の module で返し、
// 先に addInitScript で置いた window.wails.Call.ByName が fixture を返す。src は page.route で架空の origin から配る
// (file:// では ES module の import が cross-origin で拒まれる)。
import { after, before, describe, test } from "node:test";
import assert from "node:assert/strict";
import { mkdir, readFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { chromium } from "playwright";

const here = path.dirname(fileURLToPath(import.meta.url));
const SRC = path.join(here, "..", "src");
const FIXTURE = path.join(here, "fixtures", "deps.json");
const SHOTS = path.resolve(process.env.SCREENSHOT_DIR || path.join(here, "..", "..", "..", "tmp", "gui-screenshots"));
const ORIGIN = "http://agent-tasks.test";
// 辺の無いノードの格子の列数と popup の位置が窓の幅で変わるので、窓の大きさと時刻を固定する。
const VIEWPORT = { width: 1280, height: 800 };
const NOW = new Date("2026-11-07T12:00:00+09:00");

let browser;
let deps;

before(async () => {
  deps = await readFile(FIXTURE, "utf8");
  browser = await chromium.launch();
  await mkdir(SHOTS, { recursive: true });
});

after(async () => {
  await browser?.close();
});

// open は fixture を流した依存図を開き、最初の描画を待って page と描画中に出たエラーを返す。
async function open(viewport = VIEWPORT) {
  const context = await browser.newContext({ viewport, locale: "ja-JP", timezoneId: "Asia/Tokyo", deviceScaleFactor: 2 });
  await context.clock.setFixedTime(NOW);
  await context.addInitScript((text) => {
    const answers = { "main.Daemon.Deps": text, "main.Daemon.Restore": "", "main.Daemon.PickFolder": "" };
    window.wails = {
      Call: {
        ByName: async (name) => {
          if (!(name in answers)) throw new Error(`fixture に無い呼び出し: ${name}`);
          return answers[name];
        },
      },
      Events: { On: () => {} },
    };
  }, deps);
  await context.route(`${ORIGIN}/**`, (route) => {
    const { pathname } = new URL(route.request().url());
    if (pathname === "/wails/runtime.js") return route.fulfill({ contentType: "text/javascript", body: "" });
    return route.fulfill({ path: path.join(SRC, pathname) });
  });
  const page = await context.newPage();
  const errors = [];
  page.on("pageerror", (err) => errors.push(String(err)));
  page.on("console", (msg) => {
    if (msg.type() === "error") errors.push(msg.text());
  });
  await page.goto(`${ORIGIN}/index.html`);
  await page.locator("#graph .node").first().waitFor();
  return { page, errors, close: () => context.close() };
}

// cards は描いた箱を key → { classes, left, top } で返す。
async function cards(page) {
  const list = await page.locator("#graph .node").evaluateAll((els) =>
    els.map((e) => ({ key: e.dataset.key, classes: [...e.classList], left: parseFloat(e.style.left), top: parseFloat(e.style.top) })),
  );
  return new Map(list.map((c) => [c.key, c]));
}

const keysWith = (byKey, cls) => [...byKey.values()].filter((c) => c.classes.includes(cls)).map((c) => c.key).sort();

describe("完了を隠した既定の図", () => {
  let page;
  let errors;
  let close;
  before(async () => {
    ({ page, errors, close } = await open());
  });
  after(() => close());

  test("完了していないノードと、完了していないノードへ続く完了を除いた箱を描く", async () => {
    const byKey = await cards(page);
    assert.deepEqual([...byKey.keys()].sort(), [
      "sid:delta",
      "sid:gamma",
      "task:alpha/2",
      "task:alpha/4",
      "task:beta/1",
      "task:beta/2",
      "task:beta/3",
    ]);
    assert.deepEqual(keysWith(byKey, "halt"), ["task:beta/1"]);
    assert.deepEqual(keysWith(byKey, "gone"), ["sid:gamma"]);
    assert.deepEqual(errors, []);
  });

  test("状態の丸に状態ごとの色の class を付ける", async () => {
    const dot = (key) => page.locator(`.node[data-key="${key}"] .st`).getAttribute("class");
    assert.equal(await dot("task:alpha/2"), "st working");
    assert.equal(await dot("task:beta/1"), "st warn");
    assert.equal(await dot("sid:gamma"), "st exited");
    assert.equal(await dot("task:beta/2"), "st");
  });

  test("見える辺と、隠した完了を通り抜けた辺を描き、longest chain の辺に印を付ける", async () => {
    // ホバーの当たり (path.hit) は矢印を持たないので、矢印の付いた path だけを数える。
    assert.equal(await page.locator("svg.edges path[marker-end]").count(), 4);
    assert.equal(await page.locator("svg.edges path[marker-end].chain").count(), 2);
    assert.equal(await page.locator("svg.edges path[marker-end].folded").count(), 1);
    const hints = await page.locator("svg.edges g.hint title").allTextContents();
    assert.deepEqual(hints.sort(), ["API の形が決まるのを待つ", "テストを直す を経由"]);
  });

  test("段の浅いノードを上に、辺の無いノードを辺でつながる図の下に並べる", async () => {
    const byKey = await cards(page);
    const top = (key) => byKey.get(key).top;
    assert.ok(top("task:alpha/2") < top("task:beta/1"));
    assert.ok(top("task:beta/1") < top("task:beta/2"));
    assert.ok(top("task:beta/2") < top("task:beta/3"));
    const linkedBottom = Math.max(...["task:alpha/2", "task:alpha/4", "task:beta/1", "task:beta/2", "task:beta/3"].map(top));
    assert.ok(top("sid:delta") > linkedBottom);
    assert.equal(top("sid:delta"), top("sid:gamma"));
  });

  test("止まったノードに下流の数を、メモを置いたノードにメモの数を添える", async () => {
    assert.equal(await page.locator('.node[data-key="task:beta/1"] .bd').textContent(), "↓2 ◇1");
    assert.equal(await page.locator('.node[data-key="task:alpha/2"] .bd').textContent(), "◇1");
  });

  test("メモの一覧と凡例と footer を出す", async () => {
    assert.equal(await page.locator("#park-list .mrow").count(), 2);
    assert.equal(await page.locator("#park-title").textContent(), "メモ · 2");
    assert.equal(await page.locator("#legend").isHidden(), false);
    assert.equal(await page.locator("#completed-label").textContent(), "完了したタスクも表示 (2 件)");
    assert.match(await page.locator("#foot-text").textContent(), /daemon 動作中 .*止まっている 1 · 作業中 2 · 待ち 0 · 完了 2 \(非表示\)/);
  });

  test("スクリーンショット: 全体", async () => {
    await page.screenshot({ path: path.join(SHOTS, "graph.png") });
  });

  test("スクリーンショット: ノードを選んだときの単体ビュー", async () => {
    await page.locator('.node[data-key="task:beta/1"]').click();
    await page.locator("#pop:not([hidden])").waitFor();
    await page.screenshot({ path: path.join(SHOTS, "graph-popup.png") });
    await page.keyboard.press("Escape");
  });
});

describe("完了も表示した図", () => {
  test("完了したノードも描き、通り抜けていた辺を元の辺で描く", async () => {
    const { page, errors, close } = await open();
    try {
      await page.locator("#completed-check").check();
      const byKey = await cards(page);
      assert.equal(byKey.size, 9);
      assert.deepEqual(keysWith(byKey, "gone"), ["sid:gamma", "task:alpha/1", "task:alpha/3"]);
      assert.equal(await page.locator("svg.edges path[marker-end]").count(), 6);
      assert.equal(await page.locator("svg.edges path[marker-end].folded").count(), 0);
      assert.ok(byKey.get("task:alpha/1").top < byKey.get("task:alpha/2").top);
      await page.screenshot({ path: path.join(SHOTS, "graph-completed.png") });
      assert.deepEqual(errors, []);
    } finally {
      await close();
    }
  });
});

describe("縮小した図", () => {
  test("全体を表示で 100% より小さくし、図全体を窓に収める", async () => {
    const { page, errors, close } = await open({ width: 640, height: 480 });
    try {
      await page.locator("#zoom-fit").click();
      const label = await page.locator("#zoom-reset").textContent();
      assert.ok(parseInt(label, 10) < 100, `拡大率が ${label}`);
      const frame = await page.locator("#graph .zoom-frame").boundingBox();
      const host = await page.locator("#graph").boundingBox();
      assert.ok(frame.width <= host.width && frame.height <= host.height, "縮小した図が窓に収まっていない");
      await page.screenshot({ path: path.join(SHOTS, "graph-fit.png") });
      assert.deepEqual(errors, []);
    } finally {
      await close();
    }
  });
});
