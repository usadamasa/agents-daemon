// Package backend は環境から端末多重化ツール (rex) を選び､その multiplexer.Client を作る｡
package backend

import (
	"errors"
	"fmt"

	"github.com/usadamasa/agents-daemon/internal/multiplexer"
	"github.com/usadamasa/agents-daemon/internal/rexcli"
	"github.com/usadamasa/agents-daemon/internal/sessionstate"
)

// ErrNoRexSession は rex の外で起動され､監視する block が無いことを表す｡
// daemon --ensure はこれを受けて何もせずに終わる (素の端末の SessionStart で毎回 daemon を起こさない)｡
var ErrNoRexSession = errors.New("REX_SESSION が無いため rex の block を監視できません (rex の外で起動されました)")

// Detect は使う backend の種類を返す｡REX_SESSION があれば rex､無ければ ErrNoRexSession｡
func Detect(getenv func(string) string) (string, error) {
	if getenv("REX_SESSION") != "" {
		return sessionstate.MultiplexerKindRex, nil
	}
	return "", ErrNoRexSession
}

// New は kind の Client を作る｡block から session_id を引く対応は､statusline が
// `context/<sid>.json` に書いた記録 (store) から取る｡
func New(kind string, store sessionstate.Store) (multiplexer.Client, error) {
	resolve := func() (map[string]string, error) { return store.ListMultiplexerBlocks(kind) }
	switch kind {
	case sessionstate.MultiplexerKindRex:
		return rexcli.NewExecClient(resolve), nil
	default:
		return nil, fmt.Errorf("未対応の多重化ツール: %q", kind)
	}
}
