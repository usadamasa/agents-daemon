package verify

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/usadamasa/agents-daemon/internal/daemonstate"
)

// writeFile は path に content を書く (親ディレクトリも作る)。
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}

// writeJSONFile はテスト用に path へ v を JSON として書き込む (親ディレクトリも作る)。
func writeJSONFile(t *testing.T, path string, v any) {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	writeFile(t, path, string(data))
}

// writeFixturePIDFile / writeFixtureSnapshot は status が読む状態を作る。
//
// daemon 側の書き込み関数を呼ばないのは、それらを「テストのためだけに公開する」
// ことになり、daemon の API surface を実際の用途以上に広げてしまうため。読み取りに使う
// StatusSnapshot 型は公開されているので、書式の知識はここへ漏れない。
func writeFixturePIDFile(t *testing.T, path string, pid int) {
	t.Helper()
	writeFile(t, path, strconv.Itoa(pid)+"\n")
}

func writeFixtureSnapshot(t *testing.T, path string, snap daemonstate.StatusSnapshot) {
	t.Helper()
	writeJSONFile(t, path, snap)
}
