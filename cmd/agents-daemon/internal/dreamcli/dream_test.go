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
	"github.com/usadamasa/agents-daemon/internal/dream/policy"
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
	want := []string{"adopt", "forget", "harvest", "mine", "policy", "stage"}
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

// setPolicyConfig は XDG_CONFIG_HOME の config.json に dream.policy を書く。
func setPolicyConfig(t *testing.T, policyPaths ...string) {
	t.Helper()
	cfgHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgHome)
	body, err := json.Marshal(map[string]any{"dream": map[string]any{"policy": policyPaths}})
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(cfgHome, "agents-daemon")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), body, 0o600); err != nil {
		t.Fatal(err)
	}
}

// dream policy は config.json の dream.policy が指す規約を合わせた結果を JSON で出す。
func TestDreamPolicyCmd_PrintsMergedPolicy(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	p := filepath.Join(t.TempDir(), "policy.json")
	if err := os.WriteFile(p, []byte(`{"version": 1, "skillNamePrefixes": ["team"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	setPolicyConfig(t, p)

	var out bytes.Buffer
	root := testRoot()
	root.SetOut(&out)
	root.SetArgs([]string{"dream", "policy"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	var got policy.Policy
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("not JSON: %q (%v)", out.String(), err)
	}
	if !slices.Equal(got.Sources, []string{p}) || !slices.Equal(got.SkillNamePrefixes, []string{"team"}) {
		t.Errorf("policy = %+v", got)
	}
}

// キーが無ければ空の規約を出す (エラーにしない)。
func TestDreamPolicyCmd_NoKey(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	var out bytes.Buffer
	root := testRoot()
	root.SetOut(&out)
	root.SetArgs([]string{"dream", "policy"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	var got policy.Policy
	if err := json.Unmarshal(out.Bytes(), &got); err != nil || len(got.Sources) != 0 {
		t.Errorf("policy = %q (%v)", out.String(), err)
	}
}

// 規約が読めなければ harvest は PII の規則を欠いたまま書き出さず、エラーで止まる。
func TestDreamHarvestCmd_FailsOnUnreadablePolicy(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)
	setPolicyConfig(t, filepath.Join(t.TempDir(), "missing.json"))

	root := testRoot()
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"dream", "harvest", "--projects-dir", t.TempDir()})
	if err := root.Execute(); err == nil {
		t.Fatal("規約が読めないのに harvest が成功した")
	}
	if _, err := os.Stat(filepath.Join(stateHome, "agents-daemon", "dream", "harvest")); !os.IsNotExist(err) {
		t.Errorf("harvest の出力が書かれている: %v", err)
	}
}

// PolicySources は doctor が規約を点検する口。読めた規約のパスか、読めない理由を返す。
func TestPolicySources(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	p := filepath.Join(t.TempDir(), "policy.json")
	if err := os.WriteFile(p, []byte(`{"version": 1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	setPolicyConfig(t, p)
	got, err := PolicySources()
	if err != nil || !slices.Equal(got, []string{p}) {
		t.Errorf("PolicySources() = (%v, %v); want ([%s], nil)", got, err, p)
	}

	setPolicyConfig(t, filepath.Join(t.TempDir(), "missing.json"))
	if _, err := PolicySources(); err == nil {
		t.Error("読めない規約なのに error が無い")
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
