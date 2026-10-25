// Package statecmd は state の置き場を読み書きして結果を JSON で出すコマンド (park、spawn、edge) の共通の走らせ方を持つ。
package statecmd

import (
	"encoding/json"
	"os"

	"github.com/spf13/cobra"

	"github.com/usadamasa/agents-daemon/internal/apppath"
)

// Run は置き場を環境から決めて fn を呼び、結果を JSON で stdout に書く。cwd が空なら今の cwd にする。
func Run(cmd *cobra.Command, cwd string, fn func(apppath.Paths, string) (any, error)) error {
	p, err := apppath.FromEnv()
	if err != nil {
		return err
	}
	if cwd == "" {
		if cwd, err = os.Getwd(); err != nil {
			return err
		}
	}
	v, err := fn(p, cwd)
	if err != nil {
		return err
	}
	return json.NewEncoder(cmd.OutOrStdout()).Encode(v)
}
