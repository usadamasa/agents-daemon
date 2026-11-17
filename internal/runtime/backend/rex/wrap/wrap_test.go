package wrap

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// peer-sessions skill は peer を起こす手順に包みと同じ本文を single quote で囲んで載せる。
// 片方だけ直してずれないよう、skill の本文に今の定数がそのまま入っていることを見る。
func TestScripts_MatchPeerSessionsSkill(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "..", "skills", "peer-sessions", "references", "rex.md"))
	if err != nil {
		t.Fatalf("skill の読み込みに失敗: %v", err)
	}
	for name, script := range map[string]string{"sessionScript": sessionScript, "tabScript": tabScript} {
		if strings.Contains(script, "'") {
			t.Errorf("%s に single quote がある (skill が single quote で囲めない): %s", name, script)
		}
		if !strings.Contains(string(b), "/bin/sh -c '"+script+"'") {
			t.Errorf("peer-sessions skill の references/rex.md に %s と同じ本文が無い:\n%s", name, script)
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
