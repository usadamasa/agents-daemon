// Package sessionstate は、statusline がセッション ID をキーに XDG state 配下へ書く
// sidecar file の置き場をまとめ、statusline の stdin から書き (IngestStatusline)、
// 保持期間を過ぎたものを消す (Prune)。
//
//	rate-limits/<session_id>.json    利用上限。書式は ratelimit が読み書き両方で持つ
//	context/<session_id>.json        context 使用率と端末多重化ツールの場所 multiplexer。
//	                                 書式は sessioncontext が読み書き両方で持つ
//
// 読むだけの側 (daemon の判定、診断、block の逆引き) は ratelimit / sessioncontext を
// 直接使い、このパッケージには依存しない。書式ごとにパッケージを分けたのは、読み手ごとに
// 要る書式が 1 つだけだったため。この形から外れるもの (statusline の cost の台帳は
// 日付で入れ子になり、日付の名前で消す) は別パッケージ (statusline/spend) に置く。
//
// rate_limits は statusline の stdin にしか来ないので、statusline を経由するのは避けられない。statusline は
// 出来事 (新しい assistant message、/compact の完了、permission mode の変更、refreshInterval) で再実行され、
// idle のセッションでは走らない。モデル単位の週次ウィンドウ (/usage の Current week (Fable)) は stdin に来ないので
// 書けない (#116)。
//
// cc-premise: 2.1.310 statusline stdin の rate_limits.five_hour / context_window と再実行の契機
// cc-premise: 2.1.310 statusline stdin の rate_limits はモデル単位の週次ウィンドウを含まない
package sessionstate

import (
	"github.com/usadamasa/agents-daemon/internal/ratelimit"
	"github.com/usadamasa/agents-daemon/internal/sessioncontext"
)

// Store は sidecar file の置き場所を持つ。ゼロ値の Store はどのディレクトリも
// 空文字列で、Prune は何もしないとして振る舞う (state を持たない呼び出し元のテストが
// そのまま書けるようにするため)。
type Store struct {
	RateLimits ratelimit.Store
	Context    sessioncontext.Store
}

// New は XDG state ディレクトリ (apppath.Paths.StateDir) から置き場所を導出する。
func New(stateDir string) Store {
	return Store{
		RateLimits: ratelimit.New(stateDir),
		Context:    sessioncontext.New(stateDir),
	}
}
