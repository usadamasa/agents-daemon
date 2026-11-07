import { test } from "node:test";
import assert from "node:assert/strict";
import { MAX_ZOOM, MIN_ZOOM, clampZoom, fitZoom, percent, scrollAfterZoom, stepZoom } from "../src/zoom.js";

test("clampZoom は MIN_ZOOM と MAX_ZOOM の間に収め、数でない値は 1 にする", () => {
  assert.equal(clampZoom(0.01), MIN_ZOOM);
  assert.equal(clampZoom(100), MAX_ZOOM);
  assert.equal(clampZoom(0.8), 0.8);
  assert.equal(clampZoom(Number.NaN), 1);
});

test("fitZoom は図全体が見える領域に収まる拡大率で、100% を超えず下限で止まる", () => {
  // 横が詰まる: 600 / 1200 = 0.5
  assert.equal(fitZoom(1200, 300, 600, 400), 0.5);
  // 縦が詰まる: 400 / 1000 = 0.4
  assert.equal(fitZoom(500, 1000, 600, 400), 0.4);
  // 小さい図は拡大しない
  assert.equal(fitZoom(100, 100, 600, 400), 1);
  // 収めると読めないほど小さくなるなら下限で止める (残りはスクロール)
  assert.equal(fitZoom(100000, 100, 600, 400), MIN_ZOOM);
  // 図か領域の大きさが 0 なら今の拡大率を変えない値 (1) を返す
  assert.equal(fitZoom(0, 0, 600, 400), 1);
  assert.equal(fitZoom(100, 100, 0, 0), 1);
});

test("stepZoom は 1 段ずつ拡大縮小し、往復すると元に戻り、範囲の端で止まる", () => {
  const up = stepZoom(1, 1);
  assert.ok(up > 1);
  assert.ok(Math.abs(stepZoom(up, -1) - 1) < 1e-9);
  assert.equal(stepZoom(MAX_ZOOM, 1), MAX_ZOOM);
  assert.equal(stepZoom(MIN_ZOOM, -1), MIN_ZOOM);
});

test("scrollAfterZoom は指した点が拡大縮小の前後で同じ画面位置に残るスクロール量を返す", () => {
  // scroll 100 で画面の 50 を指す点は図の (100 + 50) / 1 = 150。2 倍で 300 になり、画面の 50 に残すには 250。
  assert.equal(scrollAfterZoom(100, 50, 1, 2), 250);
  // 縮小: 図の (250 + 50) / 2 = 150 が 0.5 倍で 75。画面の 50 に残すには 25。
  assert.equal(scrollAfterZoom(250, 50, 2, 0.5), 25);
  // 負のスクロールにはしない
  assert.equal(scrollAfterZoom(0, 300, 1, 0.5), 0);
});

test("percent は拡大率を整数の百分率の文字列にする", () => {
  assert.equal(percent(1), "100%");
  assert.equal(percent(0.333), "33%");
  assert.equal(percent(1.25), "125%");
});
