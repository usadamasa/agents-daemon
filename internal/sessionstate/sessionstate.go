// Package sessionstate は､別プロセスがセッション ID をキーに XDG state 配下へ
// 書く sidecar file を読む唯一の窓口｡書く側もここにある｡
//
//	rate-limits/<session_id>.json    statusline / ingest-statusline が書く利用上限 (IngestStatusline)
//	context/<session_id>.json        statusline / ingest-statusline が書く context 使用率と
//	                                 端末多重化ツールの場所 multiplexer (ListMultiplexerBlocks が block から逆引きする)
//
// どちらも statusline から呼ばれた `agents-daemon` のサブコマンドか daemon がこのパッケージの
// 型で読み書きするので､書式は 1 箇所に閉じる｡
//
// 1 パッケージに置くのは､読み手から見て同じ形をしているため｡同じ root
// ディレクトリ､同じ session ID の検証､同じ mtime ベースの保持期間｡分けると
// この 3 つが分けた数だけ複製される｡この形から外れるもの (statusline の cost の台帳は
// 日付で入れ子になり､日付の名前で消す) は別パッケージ (internal/spend) に置く｡
//
// JSON の生の形 (rateLimitFile / contextFile) と､それを読む関数は非公開にして
// ある｡外から要るのは domain の型と､その解釈だけだったため｡生の形を公開すると
// epoch 秒の time.Unix 変換が呼び出し側へ散る (実際に verify で 3 箇所に散っていた)｡
package sessionstate

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/usadamasa/agents-daemon/internal/statefile"
)

// Store は sidecar file の置き場所を持つ｡ゼロ値の Store はどのディレクトリも
// 空文字列で､読み込みは「まだ何も無い」､Prune は何もしないとして振る舞う
// (state を持たない呼び出し元のテストがそのまま書けるようにするため)｡
type Store struct {
	RateLimits string
	Context    string
}

// New は XDG state ディレクトリ (apppath.Paths.StateDir) から置き場所を導出する｡
func New(stateDir string) Store {
	return Store{
		RateLimits: filepath.Join(stateDir, "rate-limits"),
		Context:    filepath.Join(stateDir, "context"),
	}
}

// sessionPath は dir 配下で sessionID に対応するファイルのパスを返す｡
// 書き出し側 (シェル) と同じ規則で､読み書き両側の規則がずれないよう組み立ては
// ここだけに置く｡sessionID が空なら ("", nil)､不正な文字を含む場合はエラーを返す｡
func sessionPath(dir, sessionID, ext string) (string, error) {
	if sessionID == "" {
		return "", nil
	}
	if !statefile.ValidSessionID(sessionID) {
		return "", fmt.Errorf("session ID %q はファイル名に使えない文字を含む", sessionID)
	}
	return filepath.Join(dir, sessionID+ext), nil
}

// fresh は観測時刻 observedAt が now から maxAge 以内かを返す｡
// observedAt が now より未来 (クロックスキュー等) の場合は信用せず false を返す｡
func fresh(observedAt, now time.Time, maxAge time.Duration) bool {
	age := now.Sub(observedAt)
	return age >= 0 && age <= maxAge
}
