package jsonflag

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// root に Add した --json は既定で通り、Unsupported を宣言したコマンド (親の宣言を含む) では RunE の前に error。
func TestAdd(t *testing.T) {
	newRoot := func(ran *[]string) *cobra.Command {
		root := &cobra.Command{Use: "agents-daemon", SilenceUsage: true, SilenceErrors: true}
		Add(root)
		run := func(cmd *cobra.Command, _ []string) error {
			*ran = append(*ran, cmd.Name())
			return nil
		}
		parent := &cobra.Command{Use: "hooks", Annotations: Unsupported()}
		parent.AddCommand(&cobra.Command{Use: "ingest", RunE: run})
		root.AddCommand(
			&cobra.Command{Use: "doctor", RunE: run},
			&cobra.Command{Use: "statusline", Annotations: Unsupported(), RunE: run},
			parent,
		)
		return root
	}

	tests := []struct {
		args    []string
		wantRun bool
	}{
		{[]string{"doctor"}, true},
		{[]string{"--json", "doctor"}, true},
		{[]string{"doctor", "--json"}, true},
		{[]string{"statusline"}, true},
		{[]string{"--json", "statusline"}, false},
		{[]string{"hooks", "ingest"}, true},
		{[]string{"--json", "hooks", "ingest"}, false},
	}
	for _, tt := range tests {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			var ran []string
			root := newRoot(&ran)
			root.SetOut(&bytes.Buffer{})
			root.SetArgs(tt.args)
			err := root.Execute()
			if tt.wantRun {
				if err != nil || len(ran) != 1 {
					t.Fatalf("Execute() error = %v, ran = %v, want 1 回動く", err, ran)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), "--json") || len(ran) != 0 {
				t.Fatalf("Execute() error = %v, ran = %v, want --json の error で RunE が動かない", err, ran)
			}
		})
	}
}

// Reject は flag の組み合わせで JSON にできないとき (daemon --foreground など) に、宣言の error と同じ形で返す。
func TestReject(t *testing.T) {
	root := &cobra.Command{Use: "agents-daemon"}
	daemon := &cobra.Command{Use: "daemon"}
	root.AddCommand(daemon)
	err := Reject(daemon, "--foreground")
	if err == nil || !strings.Contains(err.Error(), "agents-daemon daemon --foreground") || !strings.Contains(err.Error(), "--json") {
		t.Errorf("Reject() = %v, want コマンドと flag を名指しした --json の error", err)
	}
}

// Enabled は root に Add していない単体のコマンドでは false (テストで単体のコマンドを動かすため)。
func TestEnabled_withoutAdd(t *testing.T) {
	cmd := &cobra.Command{Use: "doctor"}
	if Enabled(cmd) {
		t.Error("Add していないのに Enabled が true")
	}
}
