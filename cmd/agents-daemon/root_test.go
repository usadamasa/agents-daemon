package main

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// root には全サブコマンドが載る。名前を変えると hook や skill、mod の呼び出しが黙って壊れる。
func TestRootCmd_registersAllCommands(t *testing.T) {
	var names []string
	for _, c := range newRootCmd().Commands() {
		names = append(names, c.Name())
	}
	want := []string{
		"daemon", "deps", "doctor", "dream", "edge", "event-log", "ingest-statusline", "ingest-task", "inspect",
		"logs", "park", "prune-worktrees", "simulate", "spawn", "status", "statusline", "stop", "update", "version",
	}
	slices.Sort(names)
	if !slices.Equal(names, want) {
		t.Errorf("commands = %v, want %v", names, want)
	}
	if sub, _, err := newRootCmd().Find([]string{"dream", "harvest"}); err != nil || sub.Name() != "harvest" {
		t.Errorf("Find(dream harvest) = %v, %v", sub, err)
	}
}

// version と --version は build のときに埋め込んだ plugin の version だけを 1 行で出す (hooks/build-daemon.sh)。
// 埋め込まずに建てたバイナリは dev を名乗る。
func TestRootCmd_version(t *testing.T) {
	saved := version
	t.Cleanup(func() { version = saved })
	for _, tt := range []struct{ embedded, want string }{{"2026.1025.0", "2026.1025.0\n"}, {"dev", "dev\n"}} {
		version = tt.embedded
		for _, args := range [][]string{{"version"}, {"--version"}} {
			root := newRootCmd()
			var stdout bytes.Buffer
			root.SetOut(&stdout)
			root.SetErr(&bytes.Buffer{})
			root.SetArgs(args)
			if err := root.Execute(); err != nil {
				t.Fatalf("%v: %v", args, err)
			}
			if stdout.String() != tt.want {
				t.Errorf("%v (version=%s) = %q, want %q", args, tt.embedded, stdout.String(), tt.want)
			}
		}
	}
}

// error は main が 1 行だけ stderr に出す。cobra にも出させると、detach 起動した daemon の stderr (daemon.log) に
// 同じ文が 2 行残る。
func TestRootCmd_doesNotPrintErrorsItself(t *testing.T) {
	root := newRootCmd()
	var stderr bytes.Buffer
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&stderr)
	root.SetArgs([]string{"--json", "update"})
	if err := root.Execute(); err == nil {
		t.Fatal("Execute() error = nil, want error")
	}
	if stderr.Len() != 0 {
		t.Errorf("cobra が stderr に書いた: %q (main が 1 回出すので cobra は黙る)", stderr.String())
	}
}

// --json は root のグローバルオプションで、既定で全コマンドに効く。stdout を Claude Code や mod が読む hook 用の
// コマンドと、子プロセスの出力を流すだけの update は Unsupported を宣言して error にする。
// 新しいコマンドはこの表に足すまで落ちる (既定が対応なので、JSON を出し忘れたまま黙って通さない)。
func TestRootCmd_jsonFlag(t *testing.T) {
	unsupported := map[string]bool{
		"agents-daemon event-log":         true,
		"agents-daemon ingest-statusline": true,
		"agents-daemon ingest-task":       true,
		"agents-daemon statusline":        true,
		"agents-daemon update":            true,
	}
	supported := map[string]bool{
		"agents-daemon daemon":          true,
		"agents-daemon deps":            true,
		"agents-daemon doctor":          true,
		"agents-daemon dream adopt":     true,
		"agents-daemon dream forget":    true,
		"agents-daemon dream harvest":   true,
		"agents-daemon dream mine":      true,
		"agents-daemon dream stage":     true,
		"agents-daemon edge add":        true,
		"agents-daemon edge list":       true,
		"agents-daemon edge rm":         true,
		"agents-daemon inspect":         true,
		"agents-daemon logs":            true,
		"agents-daemon park add":        true,
		"agents-daemon park adopt":      true,
		"agents-daemon park discard":    true,
		"agents-daemon park list":       true,
		"agents-daemon park set":        true,
		"agents-daemon prune-worktrees": true,
		"agents-daemon simulate":        true,
		"agents-daemon spawn":           true,
		"agents-daemon status":          true,
		"agents-daemon stop":            true,
		"agents-daemon version":         true,
	}
	var leaves []string
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		if !c.HasSubCommands() {
			leaves = append(leaves, c.CommandPath())
		}
		for _, sub := range c.Commands() {
			if sub.Name() != "help" && sub.Name() != "completion" {
				walk(sub)
			}
		}
	}
	walk(newRootCmd())
	slices.Sort(leaves)
	var want []string
	for path := range unsupported {
		want = append(want, path)
	}
	for path := range supported {
		want = append(want, path)
	}
	slices.Sort(want)
	if !slices.Equal(leaves, want) {
		t.Fatalf("leaf commands = %v, want %v (--json の対応を決めて表に足す)", leaves, want)
	}

	for _, path := range leaves {
		args := append([]string{"--json"}, strings.Fields(path)[1:]...)
		sub, _, err := newRootCmd().Find(args[1:])
		if err != nil {
			t.Fatalf("Find(%v): %v", args[1:], err)
		}
		if got := rejectsJSON(sub); got != unsupported[path] {
			t.Errorf("%s: --json を拒むか = %v, want %v", path, got, unsupported[path])
		}
	}
	for path := range unsupported {
		root := newRootCmd()
		root.SetOut(&bytes.Buffer{})
		root.SetErr(&bytes.Buffer{})
		root.SetArgs(append([]string{"--json"}, strings.Fields(path)[1:]...))
		if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "--json") {
			t.Errorf("--json %s: error = %v, want --json に対応していない error", path, err)
		}
	}
}

// rejectsJSON は cmd かその親に Unsupported の宣言があるかを返す。
func rejectsJSON(cmd *cobra.Command) bool {
	for c := cmd; c != nil; c = c.Parent() {
		if c.Annotations["jsonflag.unsupported"] == "true" {
			return true
		}
	}
	return false
}

// version の --json は {"version": ...} を出す。
func TestRootCmd_versionJSON(t *testing.T) {
	saved := version
	t.Cleanup(func() { version = saved })
	version = "2026.1031.0"
	root := newRootCmd()
	var stdout bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"--json", "version"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	var got struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil || got.Version != "2026.1031.0" {
		t.Errorf("--json version = %q (%v), want {\"version\": \"2026.1031.0\"}", stdout.String(), err)
	}
}
