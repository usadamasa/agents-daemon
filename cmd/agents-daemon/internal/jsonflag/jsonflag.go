// Package jsonflag は root のグローバルオプション --json の配線を持つ。既定で全コマンドが対応し、JSON にしても
// 意味の無いコマンド (stdout を Claude Code や mod が読む hook 用のもの、子プロセスの出力を流すだけのもの) だけが
// Annotations で Unsupported を宣言する。宣言のあるコマンドに --json が付いたら RunE の前に error にする
// (文字の出力を JSON と思い込んで読む事故を避ける)。元から JSON を出すコマンド (deps、park、edge、spawn) は
// 付けても変わらない。
package jsonflag

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/spf13/cobra"
)

// Name は flag の名前。
const Name = "json"

// annotation は非対応を宣言する Annotations の key。親に付ければ子にも効く。
const annotation = "jsonflag.unsupported"

// Add は root に --json を足し、非対応を宣言したコマンドでの指定を error にする。
func Add(root *cobra.Command) {
	root.PersistentFlags().Bool(Name, false, "出力を JSON にする (hook 用のコマンドと update では error)")
	root.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		if Enabled(cmd) && unsupported(cmd) {
			return Reject(cmd, "")
		}
		return nil
	}
}

// Unsupported はコマンドが --json に対応していない宣言。cobra.Command の Annotations に置く。
func Unsupported() map[string]string {
	return map[string]string{annotation: "true"}
}

// Reject は --json に対応していない error を返す。flag の組み合わせで JSON にできないとき (daemon --foreground など) は
// what にその flag を渡す。
func Reject(cmd *cobra.Command, what string) error {
	path := cmd.CommandPath()
	if what != "" {
		path += " " + what
	}
	return fmt.Errorf("%s は --json に対応していない", path)
}

// Enabled は --json が付いているかを返す。root に Add していない (テストで単体のコマンドを動かす) ときは false。
func Enabled(cmd *cobra.Command) bool {
	on, err := cmd.Flags().GetBool(Name)
	return err == nil && on
}

// unsupported は cmd かその親に Unsupported の宣言があるかを返す。
func unsupported(cmd *cobra.Command) bool {
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
