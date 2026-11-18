module github.com/usadamasa/agents-daemon

// net/http と cgo のビルド時の脆弱性は go1.27.4 で修正された。toolchain 行は GOTOOLCHAIN=local で効かないので、
// go 行を下限にして利用者の手元のビルドも修正版に限る
go 1.27.4

require (
	github.com/coder/websocket v1.8.15
	github.com/google/jsonschema-go v0.4.3
	github.com/kballard/go-shellquote v0.0.0-20180428030007-95032a82bc51
	github.com/spf13/cobra v1.10.2
)

require (
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/spf13/pflag v1.0.9 // indirect
)
