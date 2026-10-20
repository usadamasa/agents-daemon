// Package jsonflag は root のグローバルオプション --json の配線を持つ。対応するコマンドは Annotations で宣言し、
// 宣言の無いコマンドに --json が付いたら RunE の前に error にする (文字の出力を JSON と思い込んで読む事故を避ける)。
// 元から JSON を出すコマンド (status --board、park、edge) も宣言して、付けても変わらないようにする。
// 残りのコマンドへの対応は #182。
package jsonflag

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/spf13/cobra"
)

// Name は flag の名前。
const Name = "json"

// annotation は対応を宣言する Annotations の key。親に付ければ子 (park add など) にも効く。
const annotation = "jsonflag.supported"

// Add は root に --json を足し、対応しないコマンドでの指定を error にする。
func Add(root *cobra.Command) {
	root.PersistentFlags().Bool(Name, false, "出力を JSON にする (doctor、status。対応しないコマンドでは error)")
	root.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		if Enabled(cmd) && !supported(cmd) {
			return fmt.Errorf("%s は --json に対応していない (対応は doctor と status。残りは #182)", cmd.CommandPath())
		}
		return nil
	}
}

// Supported はコマンドが --json に対応している宣言。cobra.Command の Annotations に置く。
func Supported() map[string]string {
	return map[string]string{annotation: "true"}
}

// Enabled は --json が付いているかを返す。root に Add していない (テストで単体のコマンドを動かす) ときは false。
func Enabled(cmd *cobra.Command) bool {
	on, err := cmd.Flags().GetBool(Name)
	return err == nil && on
}

// supported は cmd かその親に Supported の宣言があるかを返す。
func supported(cmd *cobra.Command) bool {
	for c := cmd; c != nil; c = c.Parent() {
		if c.Annotations[annotation] == "true" {
			return true
		}
	}
	return false
}

// Write は v を字下げ付きの JSON で w に書く。
func Write(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
