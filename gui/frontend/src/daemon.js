// Go 側の Daemon service (service.go) の呼び口。バイナリのサブコマンドを stdin の JSON で走らせる。
// アプリはセッションではないので、ログに添える操作主体は固定の "app"。

export const SESSION = "app";

// run は command を走らせ、成功なら stdout を、失敗なら理由を返す。
export async function run(command, req) {
  try {
    const stdout = await window.wails.Call.ByName("main.Daemon.Run", command, JSON.stringify(req));
    return { stdout };
  } catch (err) {
    return { failure: String(err?.message ?? err) };
  }
}

// park は操作主体を添えて command を走らせ、失敗の理由を返す (成功なら空)。
export async function park(command, req) {
  const ran = await run(command, { ...req, session_id: SESSION });
  return "failure" in ran ? ran.failure : "";
}

// openTarget は URL か絶対パスを既定のアプリで開き、失敗の理由を返す (成功なら空)。
export async function openTarget(target) {
  try {
    await window.wails.Call.ByName("main.Daemon.Open", target);
    return "";
  } catch (err) {
    return String(err?.message ?? err);
  }
}

// pickFolder は macOS のフォルダ選択の dialog を開き、選んだ絶対パスを返す (選ばなければ空)。
export async function pickFolder() {
  try {
    return await window.wails.Call.ByName("main.Daemon.PickFolder");
  } catch {
    return "";
  }
}
