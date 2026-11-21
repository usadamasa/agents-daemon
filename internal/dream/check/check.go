// Package check は dream の adopt の後に、判断の要らない点検を機械で行う。read-only で、何も直さない。
package check

import "io"

// KindDanglingLink は auto memory の [[name]] が、どの memory の frontmatter の name にも当たらないこと。
const KindDanglingLink = "dangling_link"

// Opts は check サブコマンドの実行パラメータ。
type Opts struct {
	MemoryDir string // 空なら ~/.claude/projects/<project>/memory
	Project   string // 空なら Cwd から解決
	Cwd       string

	Home   string
	Stdout io.Writer
}

// Finding は点検に落ちた 1 件。
type Finding struct {
	Kind   string `json:"kind"`
	Path   string `json:"path"`
	Line   int    `json:"line"`
	Target string `json:"target"`
}

// Result は点検の結果。stdout の行と同じものを持ち、`--json` はそのまま出す。
type Result struct {
	Findings []Finding `json:"findings"`
}

// Run は点検を回す。
func Run(opts Opts) (Result, error) {
	return Result{}, nil
}
