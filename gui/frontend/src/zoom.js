// 依存図の拡大縮小の計算。DOM に触れず、app.js が canvas の scale と CSS 変数 --zoom とスクロール量へ当てる。

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

// centerOffset は長さ frame の枠を長さ view の領域の中心へ寄せる片側の余白。はみ出す枠は寄せずにスクロールで見る。
export function centerOffset(frame, view) {
  return Math.max(0, (view - frame) / 2);
}

// overflowCenter は長さ frame の枠が長さ view の領域からはみ出すとき、枠の中心を見せるスクロール量。
export function overflowCenter(frame, view) {
  return Math.max(0, (frame - view) / 2);
}

// scrollAfterZoom は画面上の pointer (スクロール領域の左上からの距離) が指す図の点を、拡大率を from から to へ
// 変えた後も同じ画面位置に残すスクロール量。1 つの向きについて、content は拡大前の図の長さ、view は見える長さ。
// 枠が領域より小さい間は centerOffset の余白が付くので、その分を差し引いて点を求める。
export function scrollAfterZoom(scroll, pointer, from, to, content, view) {
  const point = (scroll + pointer - centerOffset(content * from, view)) / from;
  const next = point * to + centerOffset(content * to, view) - pointer;
  return Math.max(0, Math.min(next, content * to - view));
}

// ホイールの連なりの切れ目。トラックパッドの慣性は 16ms 前後の間隔で届くので、これより空いたら指を離して止まったとみる。
export const WHEEL_GAP_MS = 150;

// wheelZoomer はホイールの 1 回 ({ deltaY, modifier, t }) と今の拡大率から次の拡大率を返す関数を作る。
// 拡大縮小にしないときは null を返す。修飾キーを押して始めた連なりだけを拡大縮小にする。
// 慣性のスクロールは途中で押した修飾キー (Cmd + Tab など) を載せて届き、読み替えると 2 回の wheel で上限へ跳ねる。
export function wheelZoomer() {
  let last = -Infinity;
  let zooming = false;
  return (e, z) => {
    if (e.t - last > WHEEL_GAP_MS) zooming = e.modifier;
    last = e.t;
    if (!zooming || !e.modifier) return null;
    const delta = Math.max(-50, Math.min(50, e.deltaY));
    return clampZoom(z * Math.exp(-delta * 0.01));
  };
}

export function percent(z) {
  return `${Math.round(z * 100)}%`;
}
