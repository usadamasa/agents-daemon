// 開いたまま新しいバージョンへ切り替えるときに、画面の状態を渡して戻す。Go 側 (upgrade.go) が新しい .app の揃ったことを
// event で知らせ、ここでボタンを出し、押されたら Daemon.Upgrade に状態を渡す。新しい側は起動時に Daemon.Restore で受け取る。
//
// 戻す項目は views ({ 鍵: { get, set } }) に登録する。項目を足すときは app.js の views に 1 行足す。

const UPGRADE_EVENT = "agent-tasks:upgrade";

// capture は登録した鍵ごとに今の値を取り出す。
export function capture(views) {
  return Object.fromEntries(Object.entries(views).map(([key, v]) => [key, v.get()]));
}

// apply は保存した値を登録した鍵へ戻す。知らない鍵 (古い・新しいバージョンにしか無い項目) は飛ばし、1 つの鍵が
// 戻せなくても残りを続ける。
export function apply(views, saved) {
  if (saved === null || typeof saved !== "object") return;
  for (const [key, value] of Object.entries(saved)) {
    try {
      views[key]?.set(value);
    } catch {
      // 戻せない項目は既定のまま。
    }
  }
}

// scrollView は要素のスクロール位置の項目。戻すのは次の描画の後 (描き直すと位置が 0 に戻る)。
export function scrollView(element) {
  return {
    get: () => [element.scrollLeft, element.scrollTop],
    set: ([x, y]) => requestAnimationFrame(() => element.scrollTo(x, y)),
  };
}

// restore は切り替える前の画面の状態を受け取って views へ戻す。切り替えで起きたのでなければ何もしない。
export async function restore(views) {
  try {
    const text = await window.wails.Call.ByName("main.Daemon.Restore");
    if (text) apply(views, JSON.parse(text));
  } catch {
    // 戻せなければ既定の画面で始める。
  }
}

// watchUpgrade は新しいバージョンの知らせで button を出し、押されたら今の画面の状態を渡して切り替える。窓が勝手に
// 入れ替わらないよう、押すまでは切り替えない。失敗したら button を押せる状態に戻し、理由を title に出す。
export function watchUpgrade(views, button) {
  window.wails.Events.On(UPGRADE_EVENT, () => {
    button.hidden = false;
  });
  button.onclick = async () => {
    button.disabled = true;
    button.title = "";
    try {
      await window.wails.Call.ByName("main.Daemon.Upgrade", JSON.stringify(capture(views)));
    } catch (err) {
      button.disabled = false;
      button.title = `切り替えられなかった: ${String(err?.message ?? err)}`;
    }
  };
}
