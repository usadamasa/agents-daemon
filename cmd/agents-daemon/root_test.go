package main

import (
	"slices"
	"testing"
)

// root には全サブコマンドが載る。名前を変えると hook や skill、mod の呼び出しが黙って壊れる。
func TestRootCmd_registersAllCommands(t *testing.T) {
	var names []string
	for _, c := range newRootCmd().Commands() {
		names = append(names, c.Name())
	}
	want := []string{
		"board-log", "daemon", "doctor", "dream", "edge", "ingest-statusline", "ingest-task", "inspect",
		"logs", "park", "prune-worktrees", "simulate", "status", "statusline", "stop",
	}
	slices.Sort(names)
	if !slices.Equal(names, want) {
		t.Errorf("commands = %v, want %v", names, want)
	}
	if sub, _, err := newRootCmd().Find([]string{"dream", "harvest"}); err != nil || sub.Name() != "harvest" {
		t.Errorf("Find(dream harvest) = %v, %v", sub, err)
	}
}
