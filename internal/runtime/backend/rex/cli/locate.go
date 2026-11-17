package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
)

// installedBinDir は既定の置き場の Versions/<build>/ から rex のあるディレクトリまでの相対パス。
const installedBinDir = "Rex Server.app/Contents/MacOS"

// defaultVersionsDir は rex の既定の置き場の Versions/ を返す。rex は block の PATH に
// `Versions/Current/Rex Server.app/Contents/MacOS` の 1 項だけを載せ、ログインシェルの PATH には載らない。
// `Current` は symlink ではなく build の複製のディレクトリで、更新のたびに中身だけ入れ替わる。
// 前の数字名の build は次の更新で消え、ダウンロードの途中経過 `.staging-<UUID>/` が残ることがある。
// rex-premise: preview.30 cli block の PATH は Versions/Current/… の 1 項だけ。Current は更新で中身が入れ替わり、前の build は次の更新で消える
// rex-premise: preview.30 cli 既定の置き場は ~/Library/Application Support/com.superlogical.rex/RexServer/Versions/{Current,<build>}/Rex Server.app/Contents/MacOS/rex。ログインシェルの PATH には載らない
func defaultVersionsDir() (string, bool) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", false
	}
	return filepath.Join(home, "Library", "Application Support", "com.superlogical.rex", "RexServer", "Versions"), true
}

// locateBinary は rex のバイナリの絶対パスを返す。REX_BIN_PATH があればそのまま、無ければ PATH を探す。
// PATH で引けないとき (rex の block の外で起きたプロセス、覚えていた build が更新で消えた) は、
// 既定の置き場の Current、無ければ一番新しい数字名の build を引く。どこにも無ければ exec.ErrNotFound を wrap した error を返す。
func locateBinary() (string, error) {
	if bin := os.Getenv("REX_BIN_PATH"); bin != "" {
		return bin, nil
	}
	if bin, err := exec.LookPath("rex"); err == nil {
		return bin, nil
	}
	if versions, ok := defaultVersionsDir(); ok {
		if bin, err := exec.LookPath(filepath.Join(versions, "Current", installedBinDir, "rex")); err == nil {
			return bin, nil
		}
		if bin, ok := newestBuildIn(versions); ok {
			return bin, nil
		}
	}
	return "", fmt.Errorf("rex が PATH にも既定の置き場にも無い: %w", exec.ErrNotFound)
}

// newestBuildIn は versions の直下の数字名のディレクトリから、`<build>/Rex Server.app/Contents/MacOS/rex` が実行できる
// 一番大きい build を選ぶ。ダウンロード途中でまだ rex の無い build は飛ばす。`Current` と `.staging-…` は数字名でないので
// 候補にしない (`Current` は呼び手が先に見る)。
func newestBuildIn(versions string) (bin string, ok bool) {
	entries, err := os.ReadDir(versions)
	if err != nil {
		return "", false
	}
	best := -1
	for _, e := range entries {
		n, convErr := strconv.Atoi(e.Name())
		if convErr != nil || !e.IsDir() || n <= best {
			continue
		}
		candidate := filepath.Join(versions, e.Name(), installedBinDir, "rex")
		if _, lookErr := exec.LookPath(candidate); lookErr != nil {
			continue
		}
		best, bin = n, candidate
	}
	return bin, best >= 0
}

// Vanished は rex の起動が「バイナリが無い」で落ちたかを判定する。名前で探して無いときは
// exec.ErrNotFound、覚えていた絶対パスが消えていたときは fs.ErrNotExist になる。
func Vanished(err error) bool {
	return errors.Is(err, exec.ErrNotFound) || errors.Is(err, fs.ErrNotExist)
}
