package wrap

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// peer-sessions skill は peer を `agents-daemon spawn` で起こし、包みの本文を写さない。包みの本文は
// このパッケージだけが持つ。
func TestScripts_NotCopiedIntoPeerSessionsSkill(t *testing.T) {
	dir := filepath.Join("..", "..", "..", "..", "..", "skills", "peer-sessions")
	for _, name := range []string{"SKILL.md", filepath.Join("references", "rex.md")} {
		b, err := os.ReadFile(filepath.Join(dir, name)) // #nosec G304 -- repo の固定のファイル
		if err != nil {
			t.Fatalf("skill の読み込みに失敗: %v", err)
		}
		for script, body := range map[string]string{"sessionScript": sessionScript, "tabScript": tabScript} {
			if strings.Contains(string(b), body) {
				t.Errorf("peer-sessions skill の %s に %s の写しがある。起動は agents-daemon spawn に任せる", name, script)
			}
		}
	}
}

// 包みは sh -c の後に $0 (包みの名前)、包みが読む引数、起こす argv を並べる。argv は 1 引数ずつそのまま渡す。
func TestCommands(t *testing.T) {
	argv := []string{"claude", "--", "a b $HOME"}
	if got, want := SessionCommand("/r/rex", "/a/agents-daemon", argv), []string{"/bin/sh", "-c", sessionScript, argv0, "/r/rex", "/a/agents-daemon", "claude", "--", "a b $HOME"}; !slices.Equal(got, want) {
		t.Errorf("SessionCommand() = %q, want %q", got, want)
	}
	if got, want := TabCommand(argv), []string{"/bin/sh", "-c", tabScript, argv0, "claude", "--", "a b $HOME"}; !slices.Equal(got, want) {
		t.Errorf("TabCommand() = %q, want %q", got, want)
	}
}
