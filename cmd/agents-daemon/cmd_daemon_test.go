package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// rex の外 (REX_SESSION なし) の SessionStart は素の端末でも走る。--ensure はそこで
// daemon を起こさず、hook を失敗させない (exit 0) ことを確かめる。
func TestDaemonCmd_rexの外では起動しない(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("REX_SESSION", "")

	t.Run("--ensure は stderr に 1 行出して exit 0", func(t *testing.T) {
		root := newRootCmd()
		var stderr bytes.Buffer
		root.SetErr(&stderr)
		root.SetArgs([]string{"daemon", "--ensure"})
		if err := root.Execute(); err != nil {
			t.Fatalf("Execute() error = %v, want nil (hook を失敗させない)", err)
		}
		if !strings.Contains(stderr.String(), "REX_SESSION") {
			t.Errorf("stderr = %q, want REX_SESSION に触れる 1 行", stderr.String())
		}
		if _, err := os.Stat(filepath.Join(home, ".local", "state", "agents-daemon")); err == nil {
			t.Error("state ディレクトリが作られている (daemon を起こそうとした)")
		}
	})

	t.Run("--foreground は error", func(t *testing.T) {
		root := newRootCmd()
		root.SetErr(&bytes.Buffer{})
		root.SetArgs([]string{"daemon", "--foreground"})
		err := root.Execute()
		if err == nil || !strings.Contains(err.Error(), "REX_SESSION") {
			t.Fatalf("Execute() error = %v, want REX_SESSION に触れる error", err)
		}
	})
}
