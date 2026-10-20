package verify

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/usadamasa/agents-daemon/internal/apppath"
	"github.com/usadamasa/agents-daemon/internal/daemon"
	"github.com/usadamasa/agents-daemon/internal/ratelimit"
	"github.com/usadamasa/agents-daemon/internal/statefile"
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

// mustRateLimitFile は sessionID に対応する利用上限 state のパスを返す。
// ファイル名の組み立ては statefile.SessionPath が唯一の実装元なので、テストもそこを通す。
func mustRateLimitFile(t *testing.T, p apppath.Paths, sessionID string) string {
	t.Helper()
	path, err := statefile.SessionPath(ratelimit.New(p.StateDir()).Dir, sessionID, ".json")
	if err != nil {
		t.Fatalf("SessionPath(%q) error = %v", sessionID, err)
	}
	return path
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

func writeFixtureSnapshot(t *testing.T, path string, snap daemon.StatusSnapshot) {
	t.Helper()
	writeJSONFile(t, path, snap)
}
