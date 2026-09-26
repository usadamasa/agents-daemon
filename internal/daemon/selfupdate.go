package daemon

import (
	"fmt"
	"os"
	"time"
)

// binaryStamp は起動時に読んだ実行ファイルの識別情報｡
// mtime とサイズの両方を見るのは､ビルドが同一秒内に終わったときに mtime だけでは
// 差が出ないことがあるため｡
type binaryStamp struct {
	path    string
	modTime time.Time
	size    int64
}

// currentBinaryStamp は動作中のプロセスの実行ファイルの stamp を返す｡
func currentBinaryStamp() (binaryStamp, error) {
	exe, err := os.Executable()
	if err != nil {
		return binaryStamp{}, fmt.Errorf("実行ファイルパスの取得に失敗: %w", err)
	}
	return stampFor(exe)
}

func stampFor(path string) (binaryStamp, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return binaryStamp{}, fmt.Errorf("実行ファイルの情報取得に失敗: %w", err)
	}
	return binaryStamp{path: path, modTime: fi.ModTime(), size: fi.Size()}, nil
}

// changed は s の時点から実行ファイルが差し替わったかを返す｡
//
// 判定できない場合 (ファイルが一時的に見えない等) は false を返す｡ここで
// 誤って true を返すと､動いている daemon を不必要に落としてしまうため､
// 疑わしいときは「変わっていない」に倒す｡
func (s binaryStamp) changed() bool {
	if s.path == "" {
		return false
	}
	now, err := stampFor(s.path)
	if err != nil {
		return false
	}
	return !now.modTime.Equal(s.modTime) || now.size != s.size
}

// Go のバイナリは起動時にメモリへ読み込まれるため､hook が
// ${XDG_CACHE_HOME:-~/.cache}/agents-daemon/bin/agents-daemon を差し替えても
// (または `task install` で入れ直しても) 動作中のプロセスには反映されない｡--ensure は「生きている daemon が
// あれば何もしない」ので､放っておくと直したはずのコードが何日も動かないままになる
// (実際､送信バグを直した後も古い daemon が動き続け､その晩の上限到達で
// 自動再開に失敗した)｡
//
// そこで daemon 自身が毎 tick で実行ファイルの差し替えに気づき､後始末をしてから
// 新しい版を detach 起動して入れ替わる｡監視の切れ目を最小にするため､自分が
// 終了する前ではなく「後始末の直後」に新しい版を起こす｡
