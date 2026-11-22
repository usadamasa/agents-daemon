// Package wrap は spawn した pane の中で走る sh の包みと、包みが叩く rex のバイナリの場所を持つ。
// 包みは起こしたコマンドの終わりに後始末 (自分の session を閉じる) をし、そのために pane の中から rex の CLI を叩く。
// 本文はここだけが持つ。peer-sessions skill も `agents-daemon spawn` で起こし、本文を写さない (#636)。
// 起こし方ごとの印・閉じる主体・rename の一覧は spawn の package doc が持つ。
package wrap

// sharedSessionEnv は、タブの包みが起こすコマンドに渡す環境変数。値は 1。plugin の SessionStart hook
// (hooks/shared-rex-session.sh) がこれを見て、起きた claude に session を rename させない案内を出す。
// mod (hooks/tab-label.ts) もこれを見て、タブの名前を作業に合わせて付け直す。
const sharedSessionEnv = "AGENTS_DAEMON_SHARED_REX_SESSION"

// argv0 は包みの sh の $0。プロセス一覧で包みと分かる名前。
const argv0 = "agents-daemon-spawn"

// sessionScript は起こしたコマンドを走らせ、終わったら session に残る block が自分 (REX_BLOCK) だけのときに
// 自分の rex session を閉じる sh の本文。$1 が rex のバイナリ、$2 が agents-daemon のバイナリ、残りが起こす argv。
// rex は中のコマンドが終わっても session を閉じず、pane が閉じても window が 0 の session が残る (#391)。
//   - set -m で、起こすコマンドを自分のプロセスグループで端末の前景にする。sh と同じグループのままだと rex は sh を
//     前景と見て、claude が立てた端末の題の持ち主を sh とし、サイドバーに題を出さない (#570)。
//   - 起こす前に session の印を書く。後から開いた pane が残って自分では閉じられなかった session を、pane が空に
//     なった後で daemon が閉じる (#557)。印を書けなくてもコマンドは起こす (失敗は画面に出る)。
//   - 0 以外で終わったときは終わり方を出して Enter を待ち、それまで失敗の画面を残す。待つ間の Ctrl-C は Enter と同じ。
//   - 後から開いた pane やタブがあると session kill が確認なしで巻き込むので、そのときは閉じない (自分の pane だけ閉じる)。
//   - block ls か jq が失敗したときも閉じない側へ倒す。
//
// REX_SESSION は ID なので、起きた claude が session を rename しても当たる。
// rex-premise: preview.30 cli block ls -s は session の全 window の block を返す。中のコマンドの終了で session は閉じない
// rex-premise: preview.28 program_status サイドバーは端末の題の持ち主 (block call title の owner) が前景のプロセスのときだけ題を出す
// macOS の /bin/sh (bash 3.2) は INT の trap の後も read を続けるので、閉じる処理 (c) を trap から直に呼んで抜ける。
const sessionScript = `set -m; rex="$1"; ad="$2"; shift 2; "$ad" spawned-session mark "$REX_SESSION" >/dev/null; "$@"; s=$?; ` +
	`c() { if "$rex" --autostart=false block ls -s "$REX_SESSION" --json | ` +
	`jq -e --arg b "$REX_BLOCK" ".blocks | length == 1 and .[0].block_id == \$b" >/dev/null; ` +
	`then "$rex" --autostart=false session kill "$REX_SESSION"; fi; exit "$s"; }; ` +
	`if [ "$s" -ne 0 ]; then trap c INT; printf "\n%s が exit %s で終わった。Enter で閉じる。\n" "$1" "$s"; read -r _; fi; c`

// tabScript は既存の session に足したタブでコマンドを走らせる sh の本文。$1 以降が起こす argv。
// 足し先の session はほかの作業と共有するので、印を書かず session も閉じない。コマンドが終われば pane が閉じ、
// 最後の pane を失った window は rex が閉じるので、ほかのタブは残る。set -m (サイドバーの題) と 0 以外で終わったときの
// 待ちは sessionScript と同じ。
// rex-premise: preview.30 api new_window のコマンドが終わると pane と window が閉じ、session は残る (shell の options が既定のまま。2026-11-18 に実測)
const tabScript = `set -m; export ` + sharedSessionEnv + `=1; "$@"; s=$?; c() { exit "$s"; }; ` +
	`if [ "$s" -ne 0 ]; then trap c INT; printf "\n%s が exit %s で終わった。Enter で閉じる。\n" "$1" "$s"; read -r _; fi; c`

// SessionCommand は新しい session の pane で argv を起こす command。終わったら session を閉じる。
// rexBin は包みが叩く rex (Locate)、self は印を書く agents-daemon のパス。
func SessionCommand(rexBin, self string, argv []string) []string {
	return append([]string{"/bin/sh", "-c", sessionScript, argv0, rexBin, self}, argv...)
}

// TabCommand は既存の session に足したタブの pane で argv を起こす command。session には触れない。
func TabCommand(argv []string) []string {
	return append([]string{"/bin/sh", "-c", tabScript, argv0}, argv...)
}
