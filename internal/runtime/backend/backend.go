// Package backend は環境から端末多重化ツール (rex) を確かめ、その multiplexer.Client を作る。
package backend

import (
	"errors"
	"io"

	"github.com/usadamasa/agents-daemon/internal/multiplexer"
	"github.com/usadamasa/agents-daemon/internal/runtime/backend/rex"
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
	return kind, ConnectRex(contexts), nil
}

// Close は Connect / ConnectRex が返した Client の接続を閉じる。rex の Client は control API の接続を 1 本張ったまま
// 使い回すので、使い終えたプロセスは閉じて close frame を送る (送らずに切ると server が client を 60 秒残す)。
// 呼び手が自分の interface (spawn.Host など) で受けた値もそのまま渡せる。閉じる接続を持たない値 (テストの fake) では
// 何もしない。閉じる失敗は呼び手にできることが無いので返さない。
func Close(c any) {
	if closer, ok := c.(io.Closer); ok {
		_ = closer.Close()
	}
}

// ConnectRex は REX_SESSION を確かめずに rex の Client を作る。依存図のアプリ (gui/) は rex の外で起きて spawn / tell /
// park hand を呼ぶので、自分が rex の block にいるかは問わず、rex の server に届くかだけが問われる (届かなければ
// 呼び出しが失敗する)。daemon は Detect 付きの Connect を使う。
func ConnectRex(contexts sessioncontext.Store) multiplexer.Client {
	resolve := func() (map[string]string, error) {
		return contexts.ListMultiplexerBlocks(sessioncontext.MultiplexerKindRex)
	}
	return rex.NewExecClient(resolve)
}
