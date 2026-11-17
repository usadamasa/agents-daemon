package rex

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kballard/go-shellquote"
)

// fakeRex は spawn の包み (wrap の sessionScript) が pane の中から叩く rex の CLI の fake と、その fixture の置き場。
// daemon 自身は rex を control API だけで操作するので、CLI を叩くのは包みだけ。
type fakeRex struct {
	dir string
	bin string
}

// newFakeRex は包みが叩く 2 つのサブコマンドだけを真似る fake の rex を書く。`block ls -s <session> --json` は
// `block-ls-<session>.json` を返し、無ければ実機と同じく exit 3。`session kill` は成功する。
// 呼び出しごとに引数を 1 つずつ single quote で囲って calls.log へ追記する。--autostart=false が先頭に無い呼び出しは
// 拒むので、成功を確かめるテストはどれも包みが autostart を切っていることを確かめている。
func newFakeRex(t *testing.T) *fakeRex {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "rex")
	script := `#!/bin/sh
d=` + shellQuote(dir) + `
for a in "$@"; do
  printf "'%s' " "$(printf '%s' "$a" | sed "s/'/'\\\\''/g")"
done >> "$d/calls.log"
echo >> "$d/calls.log"
if [ "$1" != "--autostart=false" ]; then
  echo "Error: --autostart=false がない" >&2
  exit 2
fi
shift
case "$1 $2" in
  "block ls")
    if [ -f "$d/block-ls-$4.json" ]; then cat "$d/block-ls-$4.json"; else echo "Error: session \"$4\" not found" >&2; exit 3; fi ;;
  "session kill") exit 0 ;;
  *) echo "Error: unknown command $1 $2" >&2; exit 2 ;;
esac
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil { // #nosec G306 -- テストの実行ファイル
		t.Fatalf("fake rex の作成に失敗: %v", err)
	}
	return &fakeRex{dir: dir, bin: bin}
}

func (f *fakeRex) put(t *testing.T, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(f.dir, name), []byte(content), 0o600); err != nil {
		t.Fatalf("fixture %s の作成に失敗: %v", name, err)
	}
}

// splitArgs は calls.log の行を、shell の語として引数に分ける。
func splitArgs(t *testing.T, lines []string) [][]string {
	t.Helper()
	out := make([][]string, 0, len(lines))
	for _, line := range lines {
		args, err := shellquote.Split(line)
		if err != nil {
			t.Fatalf("%q を引数に分けられない: %v", line, err)
		}
		out = append(out, args)
	}
	return out
}

func readLines(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path) // #nosec G304 -- テストの一時ディレクトリ
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("%s の読み込みに失敗: %v", path, err)
	}
	s := strings.TrimRight(string(b), "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
