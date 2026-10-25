// Package sessionstate は、statusline がセッション ID をキーに XDG state 配下へ書く
// sidecar file の置き場をまとめ、statusline の stdin から書き (IngestStatusline)、
// 保持期間を過ぎたものを消す (Prune)。
//
//	rate-limits/<session_id>.json    利用上限。書式は internal/ratelimit が読み書き両方で持つ
//	context/<session_id>.json        context 使用率と端末多重化ツールの場所 multiplexer。
//	                                 書式は internal/sessioncontext が読み書き両方で持つ
//
// 読むだけの側 (daemon の判定、診断、block の逆引き) は ratelimit / sessioncontext を
// 直接使い、このパッケージには依存しない。書式ごとにパッケージを分けたのは、読み手ごとに
// 要る書式が 1 つだけだったため。この形から外れるもの (statusline の cost の台帳は
// 日付で入れ子になり、日付の名前で消す) は別パッケージ (internal/statusline/spend) に置く。
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
