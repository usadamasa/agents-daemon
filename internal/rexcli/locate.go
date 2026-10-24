package rexcli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// versionsComponent は rex の自動更新がバイナリを置くディレクトリの、build 番号の 1 つ上の名前。
// rex は block の PATH に `…/RexServer/Versions/Current/Rex Server.app/Contents/MacOS` と、同じ形で `<build>` (数字名) の
// 項を載せる。`Current` は symlink ではなく build の複製のディレクトリで、更新のたびに中身だけ入れ替わる。
// 前の数字名の build は次の更新で消え、ダウンロードの途中経過 `.staging-<UUID>/` が残ることがある。
// `RexServer/CLI/` は存在するが空。
// rex-premise: preview.19 cli block の PATH は Versions/Current/… と Versions/<build>/… の順。Current は更新で中身が入れ替わり、前の build は次の更新で消える
const versionsComponent = "Versions"

// locateBinary は rex のバイナリの絶対パスを返す。REX_BIN_PATH があればそのまま、無ければ PATH を探す。
// PATH の項が全部外れたとき (`Versions/<build>/…` だけを持つ PATH で、その build が更新で消えた) に限り、
// 同じ置き場の一番新しい数字名の build を引く。どこにも無ければ exec.ErrNotFound を wrap した error を返す。
func locateBinary() (string, error) {
	if bin := os.Getenv("REX_BIN_PATH"); bin != "" {
		return bin, nil
	}
	if bin, err := exec.LookPath("rex"); err == nil {
		return bin, nil
	}
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if bin, ok := newestBuild(dir); ok {
			return bin, nil
		}
	}
	return "", fmt.Errorf("rex が PATH にも Versions/<build>/ にも無い: %w", exec.ErrNotFound)
}

// newestBuild は PATH の項 dir を `<prefix>/Versions/<build>/<suffix>` と読み、`<prefix>/Versions/` の中で
// `<suffix>/rex` を持つ一番大きい build 番号の rex を返す。dir がその形でなければ ok=false。
func newestBuild(dir string) (bin string, ok bool) {
	parts := strings.Split(filepath.Clean(dir), string(filepath.Separator))
	for i, p := range parts {
		if p != versionsComponent || i+1 >= len(parts) {
			continue
		}
		versions := filepath.Join(parts[:i+1]...)
		if filepath.IsAbs(dir) {
			versions = string(filepath.Separator) + versions
		}
		suffix := filepath.Join(parts[i+2:]...)
		if bin, ok := newestBuildIn(versions, suffix); ok {
			return bin, true
		}
	}
	return "", false
}

// newestBuildIn は versions の直下の数字名のディレクトリから、`<build>/<suffix>/rex` が実行できる一番大きい build を選ぶ。
// ダウンロード途中でまだ rex の無い build は飛ばす。`Current` と `.staging-…` は数字名でないので候補にしない
// (`Current` の中身は一番新しい数字名の build と同じ)。
func newestBuildIn(versions, suffix string) (bin string, ok bool) {
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
		candidate := filepath.Join(versions, e.Name(), suffix, "rex")
		if _, lookErr := exec.LookPath(candidate); lookErr != nil {
			continue
		}
		best, bin = n, candidate
	}
	return bin, best >= 0
}

// binaryVanished は rex の呼び出しが「バイナリが無い」で落ちたかを判定する。名前で探して無いときは
// exec.ErrNotFound、覚えていた絶対パスが消えていたときは fs.ErrNotExist になる。
func binaryVanished(err error) bool {
	return errors.Is(err, exec.ErrNotFound) || errors.Is(err, fs.ErrNotExist)
}
