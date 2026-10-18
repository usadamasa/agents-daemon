package main

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestDreamCmd_HasFivePhases(t *testing.T) {
	cmd := newDreamCmd()
	var names []string
	for _, sub := range cmd.Commands() {
		names = append(names, sub.Name())
	}
	slices.Sort(names)
	want := []string{"adopt", "forget", "harvest", "mine", "stage"}
	if !slices.Equal(names, want) {
		t.Errorf("subcommands = %v; want %v", names, want)
	}
}

func TestDreamCmd_IsRegisteredOnRoot(t *testing.T) {
	root := newRootCmd()
	sub, _, err := root.Find([]string{"dream", "harvest"})
	if err != nil {
		t.Fatalf("Find(dream harvest): %v", err)
	}
	if sub.Name() != "harvest" {
		t.Errorf("Find(dream harvest) = %q", sub.Name())
	}
}

func TestChildArgs_DropsOnlyDetachFlag(t *testing.T) {
	got := childArgs([]string{"dream", "harvest", "--incremental", "--detach", "--days", "7"})
	want := []string{"dream", "harvest", "--incremental", "--days", "7"}
	if !slices.Equal(got, want) {
		t.Errorf("childArgs = %v; want %v", got, want)
	}
}

func TestDreamHarvestCmd_FailureLineIsTimestamped(t *testing.T) {
	// state の親を通常ファイルにして harvest-state.json の保存を失敗させる。
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_STATE_HOME", filepath.Join(blocker, "state"))
	t.Setenv("HOME", t.TempDir())

	var stderr bytes.Buffer
	root := newRootCmd()
	root.SetErr(&stderr)
	root.SetArgs([]string{"dream", "harvest", "--projects-dir", t.TempDir()})
	if err := root.Execute(); err == nil {
		t.Fatal("state を書けないのに成功した")
	}
	line := stderr.String()
	if !strings.Contains(line, " dream harvest: ") {
		t.Errorf("失敗の行に時刻と名前が無い: %q", line)
	}
	if strings.HasPrefix(line, "Error:") {
		t.Errorf("cobra の既定の Error: が出ている: %q", line)
	}
	if _, err := time.Parse(time.RFC3339, strings.SplitN(line, " ", 2)[0]); err != nil {
		t.Errorf("先頭が RFC3339 の時刻でない: %q", line)
	}
}

func TestDreamStageCmd_HasOutFlagInsteadOfExplorationsDir(t *testing.T) {
	cmd := newDreamCmd()
	stage, _, err := cmd.Find([]string{"stage"})
	if err != nil {
		t.Fatal(err)
	}
	if stage.Flags().Lookup("out") == nil {
		t.Error("stage に --out が無い")
	}
	if stage.Flags().Lookup("explorations-dir") != nil {
		t.Error("stage に --explorations-dir が残っている")
	}
}
