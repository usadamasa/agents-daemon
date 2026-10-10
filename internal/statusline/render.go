package statusline

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// 色は明るい背景でも読めるよう bold を付けた vivid 系｡statusline.sh と同じ値｡
const (
	pink   = "\033[1;38;5;198m"
	sky    = "\033[1;38;5;31m"
	green  = "\033[1;38;5;35m"
	purple = "\033[1;38;5;128m"
	orange = "\033[1;38;5;202m"
	red    = "\033[1;38;5;196m"
	gray   = "\033[38;5;244m"
	reset  = "\033[0m"

	separator = gray + " ･ﾟ " + reset
)

// SpendView は cost の合算 (USD) と月額上限｡rate_limits が届かない契約 (Enterprise の seat) で
// 利用上限の代わりに出す｡上限が無ければ View.Spend は nil で､項目ごと出さない｡
type SpendView struct {
	Day   float64
	Month float64
	Cap   int
}

// CacheView は prompt cache の状態｡Remaining が 0 以下なら失効済み｡
// 状態が分からない (transcript が無い､初回ターン) なら View.Cache は nil｡
type CacheView struct {
	Remaining time.Duration
}

// View は描画に要るものを全て解決した形｡stdin の値 (Input) に､呼び出し側が settings・git・
// state から引いた値を足す｡Render はここから先でファイルもコマンドも触らない｡
type View struct {
	Input
	Mode Mode
	// Home はパス表示で ~ に畳む起点｡
	Home string
	// Effort は settings.json の effortLevel｡無ければ空で項目を出さない｡
	Effort string
	// Branch は CurrentDir の branch (GitBranch の値)｡空なら項目を出さない｡
	Branch string
	// ProjectBranch は起動元 ProjectDir の branch (ProjectBranch の値)｡CurrentDir が起動元の外にあるときだけ使う｡
	ProjectBranch string
	Spend         *SpendView
	Cache         *CacheView
}

// segment は statusline の 1 項目｡色と､絵文字を含む本文｡
type segment struct {
	color string
	text  string
}

// segmentFunc は View から 1 項目を組む｡出す条件を満たさなければ false｡
type segmentFunc func(v View, now time.Time) (segment, bool)

// fullLines は full (3 行) の並び｡課金まわり (利用上限 / cost) と context を 1 行へ固めると､
// 数字を横断で読むのに視線が飛ばず､どの行も折り返さない長さに収まる｡
//
//	1 行目: 何で動いているか (model / effort / output style / version)
//	2 行目: どれだけ使ったか (context / 利用上限 / cache / cost)
//	3 行目: どこに居るか (dir / branch / worktree / 起動元)
var fullLines = [][]segmentFunc{
	{modelSegment, effortSegment, outputStyleSegment, versionSegment},
	{contextSegment, rateLimitSegment, cacheSegment, costSegment, daySpendSegment, monthSpendSegment},
	{dirSegment, branchSegment, worktreeSegment, homeSegment},
}

// compactLine は幅が足りないときの 1 行｡残すのは pane を跨いで見分けが要るもの
// (context / effort / ディレクトリ / branch / worktree) だけ｡model・cost・version・output_style は落とす｡
// 利用上限 (⏳) と月額 (🧾) はアカウント全体の値で pane ごとに変わらないので落とす｡
// cache (⏱) は pane ごとの値だが､幅を優先して落とす｡
var compactLine = []segmentFunc{contextSegment, effortSegment, shortDirSegment, plainBranchSegment, worktreeSegment}

// Render は Mode に応じた行を返す｡full は 3 行を改行で繋ぎ､末尾に改行を付けない｡
// now は利用上限のリセットまでの残り時間に使う｡
func (v View) Render(now time.Time) string {
	switch v.Mode {
	case ModeHidden:
		return ""
	case ModeCompact:
		return renderLine(v, now, compactLine)
	default:
		lines := make([]string, 0, len(fullLines))
		for _, segments := range fullLines {
			lines = append(lines, renderLine(v, now, segments))
		}
		return strings.Join(lines, "\n")
	}
}

// renderLine は 1 行ぶんの項目を左から組み､区切りで繋ぐ｡
func renderLine(v View, now time.Time, segments []segmentFunc) string {
	parts := make([]string, 0, len(segments))
	for _, build := range segments {
		if s, ok := build(v, now); ok {
			parts = append(parts, s.color+s.text+reset)
		}
	}
	return strings.Join(parts, separator)
}

// levelColor は使用率 (0..100) を色にする｡context・5 時間ウィンドウ・月額上限で共通の閾値｡
// 月額上限はハードストップ (超えると次のリセットか admin の引き上げまで止まる) なので､
// 利用上限と同じ閾値で寄せる｡
func levelColor(percent int) string {
	switch {
	case percent < 50:
		return green
	case percent < 80:
		return orange
	default:
		return red
	}
}

// --- 1 行目: 何で動いているか ---

func modelSegment(v View, _ time.Time) (segment, bool) {
	return segment{pink, "🌸 " + v.Model + " " + modelEmoji(v.Model)}, true
}

// modelEmoji はモデル名から絵文字を選ぶ｡"Opus 4.7" / "claude-opus-4-8[1m]" など形式が揺れるため
// 大小文字を無視する｡
func modelEmoji(model string) string {
	lower := strings.ToLower(model)
	switch {
	case strings.Contains(lower, "opus"):
		return "💎"
	case strings.Contains(lower, "sonnet"):
		return "🎵"
	case strings.Contains(lower, "haiku"):
		return "🍃"
	default:
		return "🤖"
	}
}

// effortColors は effort level の色｡低いほど淡く､高いほど熱く｡未知の値は灰色｡
var effortColors = map[string]string{
	"low":    gray,
	"medium": sky,
	"high":   green,
	"xhigh":  orange,
	"max":    red,
}

func effortSegment(v View, _ time.Time) (segment, bool) {
	if v.Effort == "" {
		return segment{}, false
	}
	color, ok := effortColors[v.Effort]
	if !ok {
		color = gray
	}
	return segment{color, "⚡ " + v.Effort}, true
}

func outputStyleSegment(v View, _ time.Time) (segment, bool) {
	if v.OutputStyle == "" || v.OutputStyle == "default" {
		return segment{}, false
	}
	return segment{purple, "🎀 " + v.OutputStyle}, true
}

func versionSegment(v View, _ time.Time) (segment, bool) {
	if v.Version == "" {
		return segment{}, false
	}
	return segment{gray, "🐣 v" + v.Version}, true
}

// --- 2 行目: どれだけ使ったか ---

func contextSegment(v View, _ time.Time) (segment, bool) {
	return segment{levelColor(v.ContextPercent), fmt.Sprintf("🧠 %dk (%d%%)", v.ContextTokens/1000, v.ContextPercent)}, true
}

// rateLimitSegment は 5 時間 / 7 日ウィンドウの使用率｡rate_limits が届いたセッションにだけ出す｡
// 色は 5 時間ウィンドウの使用率に従う (詰まるのは常にこちらが先)｡
func rateLimitSegment(v View, now time.Time) (segment, bool) {
	if v.FiveHourPercent == nil {
		return segment{}, false
	}
	text := fmt.Sprintf("⏳ 5h %d%%", *v.FiveHourPercent)
	if v.FiveHourResetsAt != nil {
		text += " (↻" + formatCountdown(v.FiveHourResetsAt.Sub(now)) + ")"
	}
	if v.SevenDayPercent != nil {
		text += fmt.Sprintf(" 7d %d%%", *v.SevenDayPercent)
	}
	return segment{levelColor(*v.FiveHourPercent), text}, true
}

// formatCountdown は残り時間を "1h52m" か "47m" にする｡過ぎていれば "0m"｡
func formatCountdown(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	if h > 0 {
		return fmt.Sprintf("%dh%dm", h, m)
	}
	return fmt.Sprintf("%dm", m)
}

// cacheWarnRemaining は prompt cache の残りがこれ以下なら orange にする境界｡
const cacheWarnRemaining = 10 * time.Minute

// cacheSegment は prompt cache の失効までの残り時間｡失効後の最初の prompt は会話全体を送り直すので､
// 離席の前に見えるようにする｡失効済みは cold｡
func cacheSegment(v View, _ time.Time) (segment, bool) {
	if v.Cache == nil {
		return segment{}, false
	}
	switch remaining := v.Cache.Remaining; {
	case remaining <= 0:
		return segment{red, "⏱ cache cold"}, true
	case remaining <= cacheWarnRemaining:
		return segment{orange, fmt.Sprintf("⏱ cache %dm", int(remaining.Minutes()))}, true
	default:
		return segment{green, fmt.Sprintf("⏱ cache %dm", int(remaining.Minutes()))}, true
	}
}

func costSegment(v View, _ time.Time) (segment, bool) {
	return segment{gray, fmt.Sprintf("💰 $%.2f", v.Cost)}, true
}

func daySpendSegment(v View, _ time.Time) (segment, bool) {
	if v.Spend == nil {
		return segment{}, false
	}
	return segment{gray, fmt.Sprintf("📅 $%.2f", v.Spend.Day)}, true
}

// monthSpendSegment は当月の消費と月額上限｡claude.ai と connectors の消費を含まない過小評価なので
// ~ を付けて概算だと示す｡
func monthSpendSegment(v View, _ time.Time) (segment, bool) {
	if v.Spend == nil {
		return segment{}, false
	}
	percent := 0
	if v.Spend.Cap > 0 {
		percent = roundPercent(v.Spend.Month / float64(v.Spend.Cap) * 100)
	}
	return segment{levelColor(percent), fmt.Sprintf("🧾 ~$%.2f / $%s (%d%%)", v.Spend.Month, formatCap(v.Spend.Cap), percent)}, true
}

// formatCap は月額上限を "10k" / "1.5k" / "300" にする｡
func formatCap(capUSD int) string {
	if capUSD >= 1000 {
		return fmt.Sprintf("%gk", float64(capUSD)/1000)
	}
	return fmt.Sprintf("%d", capUSD)
}

// --- 3 行目: どこに居るか ---

// dirColor は起動元の外に居るとき orange にして可視化する｡
func dirColor(v View) string {
	if OutsideProject(v.CurrentDir, v.ProjectDir) {
		return orange
	}
	return sky
}

func dirSegment(v View, _ time.Time) (segment, bool) {
	return segment{dirColor(v), "📁 " + collapseHome(v.CurrentDir, v.Home)}, true
}

// shortDirSegment は compact 用｡フルパスをやめて末尾 1 要素にする｡
func shortDirSegment(v View, _ time.Time) (segment, bool) {
	dir := collapseHome(v.CurrentDir, v.Home)
	if base := filepath.Base(dir); base != "." && base != string(filepath.Separator) {
		dir = base
	}
	return segment{dirColor(v), "📁 " + dir}, true
}

// collapseHome は home 配下のパスの先頭を ~ にする｡
func collapseHome(dir, home string) string {
	if home == "" {
		return dir
	}
	if dir == home {
		return "~"
	}
	if strings.HasPrefix(dir, home+"/") {
		return "~" + dir[len(home):]
	}
	return dir
}

func branchSegment(v View, _ time.Time) (segment, bool) {
	if v.Branch == "" {
		return segment{}, false
	}
	return segment{green, "🌿 " + v.Branch + " ✨"}, true
}

// plainBranchSegment は compact 用｡飾りを落とす｡
func plainBranchSegment(v View, _ time.Time) (segment, bool) {
	if v.Branch == "" {
		return segment{}, false
	}
	return segment{green, "🌿 " + v.Branch}, true
}

// worktreeSegment は linked worktree 名｡Claude Code が workspace.git_worktree で渡す｡
func worktreeSegment(v View, _ time.Time) (segment, bool) {
	if v.Worktree == "" {
		return segment{}, false
	}
	return segment{purple, "🌲 " + v.Worktree}, true
}

// homeSegment は起動元プロジェクト (「ホーム」)｡cwd が外に居るときだけ､名前と branch を添えて
// 2 つの世界を一目で見せる｡
func homeSegment(v View, _ time.Time) (segment, bool) {
	if !OutsideProject(v.CurrentDir, v.ProjectDir) {
		return segment{}, false
	}
	return segment{gray, "🏠 " + filepath.Base(v.ProjectDir) + " (" + v.ProjectBranch + ")"}, true
}
