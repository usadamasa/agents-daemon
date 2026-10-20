package main

import (
	"bytes"
	"slices"
	"strings"
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

// --json は root のグローバルオプション。対応するのは読むだけの診断の doctor と status と、元から JSON を出す
// park / edge。それ以外に付けると RunE の前に error で止まる (#182 で残りに広げる)。
func TestRootCmd_jsonFlag(t *testing.T) {
	root := newRootCmd()
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"--json", "stop"})
	if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "--json") {
		t.Errorf("--json stop: error = %v, want --json に対応していない error", err)
	}
	for _, name := range []string{"doctor", "status", "park", "edge"} {
		sub, _, err := newRootCmd().Find([]string{name})
		if err != nil {
			t.Fatalf("Find(%s): %v", name, err)
		}
		if sub.Annotations["jsonflag.supported"] != "true" {
			t.Errorf("%s が --json の対応を宣言していない", name)
		}
	}
}
