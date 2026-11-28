// Package rexplace は「自分の居る rex の場所」を環境から読む唯一の入口。
// rex が環境変数や名指しの方式を変えたときに直すのはここだけにし、ほかのパッケージは Place を受ける。
// block から session を引く操作は rex の backend (ResolveBlock) が持つ。
package rexplace

// BlockEnv は rex が端末に入れる、その端末の block の ID の環境変数。
// 入口の外では、子の環境を組む側 (release-verify) とテストだけが名前を使う。
// rex-premise: preview.37 cli 新しい端末には REX_BLOCK だけが入り、REX_SESSION は入らない (Server & CLI preview.23)
const BlockEnv = "REX_BLOCK"

// Place は自分の居る rex の場所。session は持たない (rex は session の位置に block ID を受け、要るときは block から引く)。
type Place struct {
	Block string
}

// FromEnv は環境から場所を読む。空の値は無いものとして扱い、rex の外なら false。
func FromEnv(getenv func(string) string) (Place, bool) {
	if block := getenv(BlockEnv); block != "" {
		return Place{Block: block}, true
	}
	return Place{}, false
}
