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
		"logs", "park", "prune-worktrees", "simulate", "spawn", "status", "statusline", "stop",
	}
	slices.Sort(names)
	if !slices.Equal(names, want) {
		t.Errorf("commands = %v, want %v", names, want)
	}
	if sub, _, err := newRootCmd().Find([]string{"dream", "harvest"}); err != nil || sub.Name() != "harvest" {
		t.Errorf("Find(dream harvest) = %v, %v", sub, err)
	}
}

// error は main が 1 行だけ stderr に出す。cobra にも出させると、detach 起動した daemon の stderr (daemon.log) に
// 同じ文が 2 行残る。
func TestRootCmd_doesNotPrintErrorsItself(t *testing.T) {
	root := newRootCmd()
	var stderr bytes.Buffer
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&stderr)
	root.SetArgs([]string{"--json", "stop"})
	if err := root.Execute(); err == nil {
		t.Fatal("Execute() error = nil, want error")
	}
	if stderr.Len() != 0 {
		t.Errorf("cobra が stderr に書いた: %q (main が 1 回出すので cobra は黙る)", stderr.String())
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
