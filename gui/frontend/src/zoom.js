// 依存図の拡大縮小の計算。DOM に触れず、app.js が #graph の CSS 変数 --zoom とスクロール量へ当てる。

// 下限は箱の文字 (13px) がおおよそ 4px で、題の形が追える最小に置く。全体表示がこれを下回る図は下限で止めてスクロールで見る。
export const MIN_ZOOM = 0.3;
export const MAX_ZOOM = 2;
// 1 段の倍率。往復で元へ戻るように掛け算で進める。
const STEP = 1.2;

export function clampZoom(z) {
  if (!Number.isFinite(z)) return 1;
  return Math.min(MAX_ZOOM, Math.max(MIN_ZOOM, z));
}

// fitZoom は contentW × contentH の図が viewW × viewH に収まる拡大率。小さい図は 100% より大きくしない。
export function fitZoom(contentW, contentH, viewW, viewH) {
  if (!(contentW > 0 && contentH > 0 && viewW > 0 && viewH > 0)) return 1;
  return clampZoom(Math.min(viewW / contentW, viewH / contentH, 1));
}

// stepZoom は dir (1 で拡大、-1 で縮小) の向きへ 1 段進める。
export function stepZoom(z, dir) {
  return clampZoom(z * STEP ** dir);
}

// scrollAfterZoom は画面上の pointer (スクロール領域の左上からの距離) が指す図の点を、拡大率を from から to へ
// 変えた後も同じ画面位置に残すスクロール量。
export function scrollAfterZoom(scroll, pointer, from, to) {
  return Math.max(0, ((scroll + pointer) * to) / from - pointer);
}

export function percent(z) {
  return `${Math.round(z * 100)}%`;
}
