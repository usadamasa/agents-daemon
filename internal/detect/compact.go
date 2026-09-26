package detect

import (
	"regexp"
	"slices"
	"strings"
)

// compact 直後に、作業が終わっていないのにセッションが入力待ちで止まることがある。
// 上限とは無関係で limit の文言も出ないため、二重シグナルによる検知には一切かからない。
//
// 代わりの裏取りは「compact の境界より後ろに Claude の応答が 1 行も無い」こと。
// これが「圧縮しただけで何も進んでいない」と「圧縮してから続きを流した」を分ける。
// 呼び出し元 (monitor) はこれに加えて agent_status と画面の安定を要求する。
var (
	// compactBoundaryPatterns は圧縮の境界行。実際の UI 行は必ず括弧付きのヒント
	// ("(ctrl+o …)") を伴うので、括弧を必須にしてある。これが無いと、この語に
	// 触れただけの散文 (README や会話) を境界と誤認して、無関係な pane へ
	// プロンプトを打ち込む事故になる。
	compactBoundaryPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)conversation compacted` + sp + `*\(`),
		regexp.MustCompile(`(?i)^` + sp + `*compacted` + sp + `+\(`),
	}

	// assistantOutputRe は Claude の応答行。⎿ (ツール結果・ヒント) は含めない。
	// compact 直後にも Tip 行が出ることがあり、それを応答と数えると検知が
	// 黙って効かなくなる。
	assistantOutputRe = regexp.MustCompile(`^\s*⏺`)

	// emptyPromptRe は何も入力されていない入力行。ユーザーが書きかけの
	// テキストを持っている pane へ送ると、その入力を巻き込んで送信してしまう。
	emptyPromptRe = regexp.MustCompile(`^` + sp + `*[│|]?` + sp + `*[❯>]` + sp + `*[│|]?` + sp + `*$`)
)

// sp は TUI が描いた行に対して使う空白の文字クラス。
//
// \s だけでは実機に当たらない。Claude Code の入力行は ❯ の後を U+00A0
// (NO-BREAK SPACE) で埋めており (herdr pane read の実バイトで確認:
// e2 9d af c2 a0)、Go の \s は [\t\n\f\r ] しか含まない。\p{Zs} (Unicode の
// 空白区切り) を足して初めて一致する。
//
// 画面のどの行が NBSP を含むかは版によって変わりうるため、TUI 由来の行を
// 見る正規表現では素の \s を使わない。
const sp = `[\s\p{Zs}]`

// CompactStallSignature は screen の末尾 n 行が「compact 直後で、以後 Claude の
// 応答が無く、空の入力行で止まっている」形なら、その根拠になった範囲
// (compact 境界行から空の入力行まで) を返す。n が 0 以下なら全行を見る。
//
// 返す文字列は呼び出し元が「画面が変わっていないか」を見るための署名でもある。
// ステータスライン (入力行より下) を含めないのは、そこにコストやトークン数のような
// 更新されうる表示が並ぶため。含めると署名が永久に安定せず、検知が黙って死ぬ。
//
// これは候補判定でしかない。実際に送るかどうかは monitor が agent_status・
// 画面の安定・クールダウンを重ねて決める。
func CompactStallSignature(screen string, n int) (string, bool) {
	lines := tailLines(splitScreen(screen), n)

	boundary := -1
	for i, line := range lines {
		if matchesAny(line, compactBoundaryPatterns) {
			boundary = i
		}
	}
	if boundary < 0 {
		return "", false
	}

	after := lines[boundary+1:]
	if slices.ContainsFunc(after, assistantOutputRe.MatchString) {
		return "", false
	}
	prompt := slices.IndexFunc(after, emptyPromptRe.MatchString)
	if prompt < 0 {
		return "", false
	}
	return strings.Join(lines[boundary:boundary+1+prompt+1], "\n"), true
}

// PromptIsEmpty は screen の末尾 n 行に空の入力行があるかを返す。n が 0 以下なら全行。
//
// 「ユーザーが打ちかけていない」の判定に使う。Claude Code の TUI は入力行を 1 つしか
// 描かず、履歴に残る過去のプロンプトは必ず本文を伴うため、空の入力行が見えることは
// そのまま「今の入力欄が空」を意味する。
//
// 入力欄に文字が残っている pane へ送ってはいけない。Escape は補完メニューを閉じるだけで
// 入力行をクリアしないため、残ったテキストに送信文字列が連結される (実機で確認)。
func PromptIsEmpty(screen string, n int) bool {
	lines := tailLines(splitScreen(screen), n)
	return slices.ContainsFunc(lines, emptyPromptRe.MatchString)
}
