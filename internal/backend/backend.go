// Package backend は環境から端末多重化ツール (herdr / rex) を選び､その multiplexer.Client を作る｡
package backend

import (
	"fmt"

	"github.com/usadamasa/agents-daemon/internal/herdrcli"
	"github.com/usadamasa/agents-daemon/internal/multiplexer"
	"github.com/usadamasa/agents-daemon/internal/rexcli"
	"github.com/usadamasa/agents-daemon/internal/sessionstate"
)

// Detect は使う backend の種類を返す｡REX_SESSION があれば rex､無ければ herdr｡
// herdr の中で rex を動かすと両方の環境変数が見えるが､Claude Code の直接の親は内側の rex になる｡
func Detect(getenv func(string) string) string {
	if getenv("REX_SESSION") != "" {
		return sessionstate.MultiplexerKindRex
	}
	return sessionstate.MultiplexerKindHerdr
}

// New は kind の Client を作る｡rex は block から session を引くのに store を使う｡
func New(kind string, store sessionstate.Store) (multiplexer.Client, error) {
	switch kind {
	case sessionstate.MultiplexerKindHerdr:
		return herdrcli.NewExecClient(), nil
	case sessionstate.MultiplexerKindRex:
		return rexcli.NewExecClient(func() (map[string]string, error) {
			return store.ListMultiplexerBlocks(sessionstate.MultiplexerKindRex)
		}), nil
	default:
		return nil, fmt.Errorf("未対応の多重化ツール: %q", kind)
	}
}
