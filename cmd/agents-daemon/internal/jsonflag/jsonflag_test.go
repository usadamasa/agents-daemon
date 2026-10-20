package jsonflag

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// root に Add した --json は、宣言のあるコマンド (親の宣言を含む) では通り、無いコマンドでは RunE の前に error。
func TestAdd(t *testing.T) {
	newRoot := func(ran *[]string) *cobra.Command {
		root := &cobra.Command{Use: "agents-daemon", SilenceUsage: true, SilenceErrors: true}
		Add(root)
		run := func(cmd *cobra.Command, _ []string) error {
			*ran = append(*ran, cmd.Name())
			return nil
		}
		parent := &cobra.Command{Use: "park", Annotations: Supported()}
		parent.AddCommand(&cobra.Command{Use: "list", RunE: run})
		root.AddCommand(
			&cobra.Command{Use: "doctor", Annotations: Supported(), RunE: run},
			&cobra.Command{Use: "stop", RunE: run},
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
		{[]string{"--json", "park", "list"}, true},
		{[]string{"stop"}, true},
		{[]string{"--json", "stop"}, false},
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

// Enabled は root に Add していない単体のコマンドでは false (テストで単体のコマンドを動かすため)。
func TestEnabled_withoutAdd(t *testing.T) {
	cmd := &cobra.Command{Use: "doctor"}
	if Enabled(cmd) {
		t.Error("Add していないのに Enabled が true")
	}
}
