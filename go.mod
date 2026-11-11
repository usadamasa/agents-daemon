module github.com/usadamasa/agents-daemon

// net/http と cgo のビルド時の脆弱性は go1.27.4 で修正された。toolchain 行は GOTOOLCHAIN=local で効かないので、
// go 行を下限にして利用者の手元のビルドも修正版に限る
go 1.27.4

require (
	github.com/google/jsonschema-go v0.4.3
	github.com/kballard/go-shellquote v0.0.0-20180428030007-95032a82bc51
	github.com/spf13/cobra v1.10.2
)

require (
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/spf13/pflag v1.0.9 // indirect
	github.com/usadamasa/go-arch-metrics v0.0.0-20261111130835-1bb07a8b3141 // indirect
	golang.org/x/crypto/x509roots/fallback v0.0.0-20261020153306-9f2b28d4331a // indirect
	golang.org/x/mod v0.42.0 // indirect
	golang.org/x/sync v0.24.0 // indirect
	golang.org/x/sys v0.49.0 // indirect
	golang.org/x/telemetry v0.0.0-20261010193011-f71dc92da45e // indirect
	golang.org/x/tools v0.52.0 // indirect
	golang.org/x/vuln v1.9.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

tool (
	github.com/usadamasa/go-arch-metrics/cmd/analyze-arch-lint
	github.com/usadamasa/go-arch-metrics/cmd/analyze-modularity
	github.com/usadamasa/go-arch-metrics/cmd/vulncheck
)
