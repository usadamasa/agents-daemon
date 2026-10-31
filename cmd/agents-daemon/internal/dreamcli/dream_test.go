package dreamcli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/usadamasa/agents-daemon/cmd/agents-daemon/internal/jsonflag"
	"github.com/usadamasa/agents-daemon/internal/dream/forget"
	"github.com/usadamasa/agents-daemon/internal/dream/harvest"
)

// testRoot は main の root と同じく SilenceUsage を立てた親にコマンドを載せる。
func testRoot() *cobra.Command {
	root := &cobra.Command{Use: "agents-daemon", SilenceUsage: true}
	root.AddCommand(Commands()...)
	return root
}

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

func TestDreamHarvestCmd_FailureLineIsTimestamped(t *testing.T) {
	// state の親を通常ファイルにして harvest-state.json の保存を失敗させる。
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_STATE_HOME", filepath.Join(blocker, "state"))
	t.Setenv("HOME", t.TempDir())

	// main は Execute の返り値を 1 行印字する。cobra 自身は何も出さない (二重に出さない) ことも見る。
	var stderr bytes.Buffer
	root := testRoot()
	root.SetErr(&stderr)
	root.SetArgs([]string{"dream", "harvest", "--projects-dir", t.TempDir()})
	err := root.Execute()
	if err == nil {
		t.Fatal("state を書けないのに成功した")
	}
	if stderr.Len() != 0 {
		t.Errorf("cobra が stderr に出している (main と二重になる): %q", stderr.String())
	}
	line := err.Error()
	if !strings.Contains(line, " dream harvest: ") {
		t.Errorf("失敗の行に時刻と名前が無い: %q", line)
	}
	if _, err := time.Parse(time.RFC3339, strings.SplitN(line, " ", 2)[0]); err != nil {
		t.Errorf("先頭が RFC3339 の時刻でない: %q", line)
	}
}

// --json は各 phase の返り値を stdout に出す。harvest --detach は子の結果を待たないので --json と併用すると error。
func TestDreamCmd_JSON(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	execJSON := func(args ...string) (string, error) {
		var out bytes.Buffer
		root := testRoot()
		jsonflag.Add(root)
		root.SetOut(&out)
		root.SetErr(&bytes.Buffer{})
		root.SetArgs(append([]string{"--json", "dream"}, args...))
		err := root.Execute()
		return out.String(), err
	}

	memDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(memDir, "obsolete.md"), []byte("body"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := execJSON("forget", "--memory-dir", memDir, "--dry-run", "obsolete")
	if err != nil {
		t.Fatal(err)
	}
	var res forget.Result
	if err := json.Unmarshal([]byte(out), &res); err != nil || !res.DryRun || len(res.Paths) != 1 {
		t.Errorf("forget --json = %q (%v)", out, err)
	}

	out, err = execJSON("harvest", "--projects-dir", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var sum harvest.Summary
	if err := json.Unmarshal([]byte(out), &sum); err != nil || sum.Out == "" {
		t.Errorf("harvest --json = %q (%v)", out, err)
	}

	if _, err := execJSON("harvest", "--detach"); err == nil || !strings.Contains(err.Error(), "--json") {
		t.Errorf("harvest --detach: error = %v, want --json に対応していない error", err)
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
