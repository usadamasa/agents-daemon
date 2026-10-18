module github.com/usadamasa/agents-daemon

go 1.26.3

require (
	github.com/kballard/go-shellquote v0.0.0-20180428030007-95032a82bc51
	github.com/spf13/cobra v1.10.2
)

require (
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/spf13/pflag v1.0.9 // indirect
	github.com/usadamasa/go-arch-metrics v0.0.0-20261018064030-bae94cae339b // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

tool (
	github.com/usadamasa/go-arch-metrics/cmd/analyze-arch-lint
	github.com/usadamasa/go-arch-metrics/cmd/analyze-modularity
)
