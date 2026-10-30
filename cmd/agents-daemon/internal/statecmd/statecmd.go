// Package statecmd は state の置き場を読み書きして結果を JSON で出すコマンド (park、spawn、edge) の共通の走らせ方を持つ。
// 入力は stdin の JSON で、形は protocol の schema が持つ。
package statecmd

import (
	"encoding/json"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/usadamasa/agents-daemon/internal/apppath"
	"github.com/usadamasa/agents-daemon/internal/protocol"
)

// Run は置き場を環境から決めて fn に stdin を渡し、結果を JSON で stdout に書く。
func Run(cmd *cobra.Command, fn func(apppath.Paths, io.Reader) (any, error)) error {
	p, err := apppath.FromEnv()
	if err != nil {
		return err
	}
	v, err := fn(p, cmd.InOrStdin())
	if err != nil {
		return err
	}
	return json.NewEncoder(cmd.OutOrStdout()).Encode(v)
}

// Decoded は stdin を schema の def の形で R に読んでから fn を呼ぶ、Run の fn を作る。
func Decoded[R any](def string, fn func(apppath.Paths, R) (any, error)) func(apppath.Paths, io.Reader) (any, error) {
	return func(p apppath.Paths, stdin io.Reader) (any, error) {
		var req R
		if err := protocol.Decode(stdin, def, &req); err != nil {
			return nil, err
		}
		return fn(p, req)
	}
}

// Cwd は入力の cwd を返す。空なら今の cwd にする。
func Cwd(cwd string) (string, error) {
	if cwd != "" {
		return cwd, nil
	}
	return os.Getwd()
}
