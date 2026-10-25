// Package backend は環境から端末多重化ツール (rex) を確かめ、その multiplexer.Client を作る。
package backend

import (
	"errors"

	"github.com/usadamasa/agents-daemon/internal/multiplexer"
	"github.com/usadamasa/agents-daemon/internal/multiplexer/rexcli"
	"github.com/usadamasa/agents-daemon/internal/sessioncontext"
)

// ErrNoRexSession は rex の外で起動され、監視する block が無いことを表す。
// daemon --ensure はこれを受けて何もせずに終わる (素の端末の SessionStart で毎回 daemon を起こさない)。
var ErrNoRexSession = errors.New("REX_SESSION が無いため rex の block を監視できません (rex の外で起動されました)")

// Detect は使う backend の種類を返す。REX_SESSION があれば rex、無ければ ErrNoRexSession。
func Detect(getenv func(string) string) (string, error) {
	if getenv("REX_SESSION") != "" {
		return sessioncontext.MultiplexerKindRex, nil
	}
	return "", ErrNoRexSession
}

// Connect は Detect で backend を確かめ、その Client を作る。block から session_id を引く対応は、
// statusline が `context/<sid>.json` に書いた記録 (contexts) から取る。
func Connect(getenv func(string) string, contexts sessioncontext.Store) (string, multiplexer.Client, error) {
	kind, err := Detect(getenv)
	if err != nil {
		return "", nil, err
	}
	resolve := func() (map[string]string, error) { return contexts.ListMultiplexerBlocks(kind) }
	return kind, rexcli.NewExecClient(resolve), nil
}
