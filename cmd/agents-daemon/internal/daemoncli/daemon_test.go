package daemoncli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/usadamasa/agents-daemon/internal/rexplace"
)

// testRoot は main の root と同じく SilenceUsage を立てた親にコマンドを載せる。
func testRoot() *cobra.Command {
	root := &cobra.Command{Use: "agents-daemon", SilenceUsage: true}
	root.AddCommand(Commands()...)
	return root
}

// rex の block の外の SessionStart は素の端末でも走る。--ensure はそこで
// daemon を起こさず、hook を失敗させない (exit 0) ことを確かめる。
func TestDaemonCmd_rexの外では起動しない(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv(rexplace.BlockEnv, "")

	t.Run("--ensure は stderr に 1 行出して exit 0", func(t *testing.T) {
		root := testRoot()
		var stderr bytes.Buffer
		root.SetErr(&stderr)
		root.SetArgs([]string{"daemon", "--ensure"})
		if err := root.Execute(); err != nil {
			t.Fatalf("Execute() error = %v, want nil (hook を失敗させない)", err)
		}
		if !strings.Contains(stderr.String(), "rex の block の外") {
			t.Errorf("stderr = %q, want rex の block の外だと伝える 1 行", stderr.String())
		}
		if _, err := os.Stat(filepath.Join(home, ".local", "state", "agents-daemon")); err == nil {
			t.Error("state ディレクトリが作られている (daemon を起こそうとした)")
		}
	})

	t.Run("--foreground は error", func(t *testing.T) {
		root := testRoot()
		root.SetErr(&bytes.Buffer{})
		root.SetArgs([]string{"daemon", "--foreground"})
		err := root.Execute()
		if err == nil || !strings.Contains(err.Error(), "rex の block の外") {
			t.Fatalf("Execute() error = %v, want rex の block の外だと伝える error", err)
		}
	})
}
