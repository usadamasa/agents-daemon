package main

import (
	"bytes"
	"testing"
)

func TestNewInspectCmd_XOR検証(t *testing.T) {
	t.Run("--pane と --file の両方指定は error", func(t *testing.T) {
		cmd := newInspectCmd()
		cmd.SetArgs([]string{"--pane", "p1", "--file", "x.txt"})
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetErr(&bytes.Buffer{})
		if err := cmd.Execute(); err == nil {
			t.Fatal("両方指定で error を期待したが nil だった")
		}
	})

	t.Run("--pane も --file も無指定は error", func(t *testing.T) {
		cmd := newInspectCmd()
		cmd.SetArgs([]string{})
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetErr(&bytes.Buffer{})
		if err := cmd.Execute(); err == nil {
			t.Fatal("無指定で error を期待したが nil だった")
		}
	})
}
