// Package config は agents-daemon の設定 (${XDG_CONFIG_HOME:-~/.config}/agents-daemon/config.json) を
// 読み込み､検証する｡ファイルが存在しない場合は全項目デフォルトとして扱い､
// 個々のキーが不正な場合もそのキーだけデフォルトへ落として起動を継続する｡
// 設定ファイルの typo で監視全体が黙って無効化される事故を避けるための方針である｡
package config

import (
	"encoding/json"
	"errors"
	"os"
	"regexp"
	"strings"
)

// 有効な readSource の値｡
//
// herdr の "detection" と "visible" は pane の viewport に収まる範囲しか返さない
// (実測: viewport 16 行の pane に --lines 150 を指定しても 16 行しか返らず､
// 古い行から順に落ちる)｡ペインを小さく分割していると判定に必要な行が画面から
// 溢れ､検知が黙って効かなくなる｡"recent" 系はスクロールバックから取るため
// 指定した行数がそのまま得られる｡
//
// この理由で "detection" は選べる値から外した｡"visible" は「今見えているものだけ」
// という別の用途があるので残すが､既定にはしない｡
const (
	ReadSourceVisible         = "visible"
	ReadSourceRecent          = "recent"
	ReadSourceRecentUnwrapped = "recent-unwrapped"
)

// pane へ送る文面の既定値｡いずれも上限の検知語 (limit / rate 等) を含めてはいけない
// (自分の送信を自分で検知して再送を繰り返す)｡
const (
	// defaultRetryMessage は上限解除後の再開で送る｡
	defaultRetryMessage = "Continue where you left off."
	// defaultCompactStallMessage は compact 後の再開で送る｡daemon は残項目を
	// 知らないので､名指しの代わりに plan file / state file を読み直させる｡
	defaultCompactStallMessage = "Continue the task you were working on. " +
		"Reread the plan file or the compact-prep state file for the open items and work through them; " +
		"if one is blocked, say what is blocking it."
	// defaultCompactAutoMessage は /compact 本体｡末尾の指示文は圧縮サマリーの
	// 保持リスト｡`/compact` 単体だと､`/compact` を接頭辞に持つ他のコマンドが
	// あるときに補完メニューが開いて Enter が候補を横取りしうるので (旧 `/compact-prep`
	// で実機確認)､後ろに文を続ける形を保つ｡
	defaultCompactAutoMessage = "/compact Keep the plan file path, current phase, and unresolved items; " +
		"keep decisions made, options tried or set aside and why, and constraints in their exact wording."
)

// 監視対象から常に除外する pane 状態｡作業中の pane に割り込まないための不変条件｡
// Config は agents-daemon の検証済み設定｡
type Config struct {
	Enabled             bool
	PollIntervalSeconds int
	MarginSeconds       int
	FallbackWaitHours   float64
	MaxRetries          int
	// RetryMessage は resume 時にペインへ送るテキスト｡入力行へエコーされるため､
	// limit / rate / overloaded のような検知語を含めてはいけない｡含めると
	// 監視が自分自身の送信を再検知して無限ループする｡
	RetryMessage            string
	ReadSource              string
	ReadLines               int
	DetectionTailLines      int
	UsedPercentageThreshold float64
	StateMaxAgeSeconds      int
	HandleTransient         bool
	TransientWaitSeconds    int
	TransientMaxWaitSeconds int
	DismissMenu             bool
	MenuDismissDelayMs      int
	SubmitDelayMs           int
	EngagedLabel            string
	CustomPatterns          []string
	CustomTransientPatterns []string
	IdleShutdownMinutes     int
	// DeferToNativeAutoContinue が true のとき､Claude Code 自身の auto-continue
	// (2.1.234 の autoContinueAtUsageLimit) がカウントダウン中の pane には手を出さない｡
	// この状態では Escape が取り消しキーなので､送信するとネイティブの継続を潰す｡
	DeferToNativeAutoContinue bool
	// CompactStallEnabled は compact 直後に入力待ちで止まった pane を突く機能の
	// 全体スイッチ｡
	CompactStallEnabled bool
	// CompactStallQuietSeconds は画面が変わらないまま何秒経ったら「止まっている」と
	// 見なすか｡compact 直後の描画や､圧縮後に Claude が考え始める間を跨がせるための猶予｡
	CompactStallQuietSeconds int
	// CompactStallCooldownMinutes は同じ pane へ続けて突かないための間隔｡
	CompactStallCooldownMinutes int
	// CompactStallMaxNudges は 1 回の停止 (compact 境界の後ろに応答が無く､空の入力行で
	// 止まっている画面が､送っても変わらずに続く状態) へ送る回数の上限｡超えたら送らずに
	// OutcomeCompactNudgeCapped をログへ残し､pane は入力待ちのまま人の判断に委ねる｡
	// 応答が流れて停止が解けたら 0 に戻し､次の停止は新しく数える｡
	CompactStallMaxNudges int
	// CompactStallMessage は compact 停止時に送るテキスト｡compact 完了 marker 経由の
	// 再開でも同じ文面を使う｡既定値と制約は defaultCompactStallMessage を参照｡
	CompactStallMessage string

	// CompactAutoEnabled は context 使用率が閾値を超えた pane へ
	// /compact-prep → /compact を投入する機能の全体スイッチ｡
	// 既定を false にしてあるのは /compact が取り消せないため｡
	CompactAutoEnabled bool
	// CompactAutoThresholdPercent はこの使用率 (0..100) 以上で投入を始める｡
	// cache read が安くなった世代では早い compact が割に合わないため､遅めに置く｡
	CompactAutoThresholdPercent float64
	// CompactAutoPrepMessage は 1 段目に送る文字列 (compact-prep の起動)｡
	// skill は plugin が配るので､既定値は plugin 名の名前空間付きで呼ぶ｡
	CompactAutoPrepMessage string
	// CompactAutoMessage は 2 段目に送る文字列 (/compact 本体)｡既定値と末尾の
	// 指示文の意図は defaultCompactAutoMessage を参照｡
	CompactAutoMessage string
	// CompactAutoPrepTimeoutMinutes は compact-prep の state file を待つ上限｡
	// 超えたら諦めて次のサイクルへ回す｡
	CompactAutoPrepTimeoutMinutes int
	// CompactAutoCooldownMinutes は同じ pane へ 1 段目を再投入しない間隔｡
	CompactAutoCooldownMinutes int

	// CompactResumeEnabled は圧縮完了 marker を見て作業を再開させる機能の
	// スイッチ｡誰が compact したか (このツール / ユーザー / 本体の autocompact) を
	// 問わず動くため､CompactAutoEnabled とは別に持つ｡
	CompactResumeEnabled bool
	// CompactResumeDelaySeconds は marker がこの秒数より古くなってから送る｡
	// 圧縮直後にユーザーが自分で続きを打つ余地を残すための猶予｡
	CompactResumeDelaySeconds int

	// CacheIdleCompactEnabled は prompt cache の失効前の compact (idle compact) のスイッチ｡
	// CompactAutoEnabled とは独立で､既定 false の理由も同じ｡
	CacheIdleCompactEnabled bool
	// CacheIdleCompactLeadSeconds は失効の何秒前から動くか｡
	CacheIdleCompactLeadSeconds int
	// CacheIdleCompactThresholdPercent はこの使用率 (0..100) 以上で動く｡
	CacheIdleCompactThresholdPercent float64
}

// Default は全項目デフォルト値の Config を返す｡
func Default() Config {
	return Config{
		Enabled:                 true,
		PollIntervalSeconds:     5,
		MarginSeconds:           60,
		FallbackWaitHours:       5,
		MaxRetries:              5,
		RetryMessage:            defaultRetryMessage,
		ReadSource:              ReadSourceRecentUnwrapped,
		ReadLines:               25,
		DetectionTailLines:      15,
		UsedPercentageThreshold: 90,
		StateMaxAgeSeconds:      900,
		HandleTransient:         true,
		TransientWaitSeconds:    60,
		TransientMaxWaitSeconds: 300,
		DismissMenu:             true,
		MenuDismissDelayMs:      300,
		SubmitDelayMs:           400,
		EngagedLabel:            "retry engaged",
		CustomPatterns:          []string{},
		CustomTransientPatterns: []string{},
		IdleShutdownMinutes:     30,

		DeferToNativeAutoContinue:   true,
		CompactStallEnabled:         true,
		CompactStallQuietSeconds:    120,
		CompactStallCooldownMinutes: 10,
		CompactStallMaxNudges:       3,
		CompactStallMessage:         defaultCompactStallMessage,

		CompactAutoEnabled:            false,
		CompactAutoThresholdPercent:   75,
		CompactAutoPrepMessage:        "/agents-daemon:compact-prep",
		CompactAutoMessage:            defaultCompactAutoMessage,
		CompactAutoPrepTimeoutMinutes: 5,
		CompactAutoCooldownMinutes:    15,

		CompactResumeEnabled:      true,
		CompactResumeDelaySeconds: 60,

		CacheIdleCompactEnabled:          false,
		CacheIdleCompactLeadSeconds:      600,
		CacheIdleCompactThresholdPercent: 40,
	}
}

// rawConfig は設定ファイルをそのまま受ける入れ物｡フィールドをポインタにしているのは
// 「キーが省略された」(nil のまま) と「キーは書かれているが値が不正」(非 nil だが
// validate で弾かれる) を区別するため｡bool のように 0 値が意味を持つ型では
// 非ポインタだと「省略」と「明示的な false/0」を取り違えてしまう｡
type rawConfig struct {
	Enabled                 *bool     `json:"enabled"`
	PollIntervalSeconds     *int      `json:"pollIntervalSeconds"`
	MarginSeconds           *int      `json:"marginSeconds"`
	FallbackWaitHours       *float64  `json:"fallbackWaitHours"`
	MaxRetries              *int      `json:"maxRetries"`
	RetryMessage            *string   `json:"retryMessage"`
	ReadSource              *string   `json:"readSource"`
	ReadLines               *int      `json:"readLines"`
	DetectionTailLines      *int      `json:"detectionTailLines"`
	UsedPercentageThreshold *float64  `json:"usedPercentageThreshold"`
	StateMaxAgeSeconds      *int      `json:"stateMaxAgeSeconds"`
	HandleTransient         *bool     `json:"handleTransient"`
	TransientWaitSeconds    *int      `json:"transientWaitSeconds"`
	TransientMaxWaitSeconds *int      `json:"transientMaxWaitSeconds"`
	DismissMenu             *bool     `json:"dismissMenu"`
	MenuDismissDelayMs      *int      `json:"menuDismissDelayMs"`
	SubmitDelayMs           *int      `json:"submitDelayMs"`
	EngagedLabel            *string   `json:"engagedLabel"`
	CustomPatterns          *[]string `json:"customPatterns"`
	CustomTransientPatterns *[]string `json:"customTransientPatterns"`
	IdleShutdownMinutes     *int      `json:"idleShutdownMinutes"`

	DeferToNativeAutoContinue   *bool   `json:"deferToNativeAutoContinue"`
	CompactStallEnabled         *bool   `json:"compactStallEnabled"`
	CompactStallQuietSeconds    *int    `json:"compactStallQuietSeconds"`
	CompactStallCooldownMinutes *int    `json:"compactStallCooldownMinutes"`
	CompactStallMaxNudges       *int    `json:"compactStallMaxNudges"`
	CompactStallMessage         *string `json:"compactStallMessage"`

	CompactAutoEnabled            *bool    `json:"compactAutoEnabled"`
	CompactAutoThresholdPercent   *float64 `json:"compactAutoThresholdPercent"`
	CompactAutoPrepMessage        *string  `json:"compactAutoPrepMessage"`
	CompactAutoMessage            *string  `json:"compactAutoMessage"`
	CompactAutoPrepTimeoutMinutes *int     `json:"compactAutoPrepTimeoutMinutes"`
	CompactAutoCooldownMinutes    *int     `json:"compactAutoCooldownMinutes"`

	CompactResumeEnabled      *bool `json:"compactResumeEnabled"`
	CompactResumeDelaySeconds *int  `json:"compactResumeDelaySeconds"`

	CacheIdleCompactEnabled          *bool    `json:"cacheIdleCompactEnabled"`
	CacheIdleCompactLeadSeconds      *int     `json:"cacheIdleCompactLeadSeconds"`
	CacheIdleCompactThresholdPercent *float64 `json:"cacheIdleCompactThresholdPercent"`
}

// Load は path から設定を読み込む｡
// ファイルが存在しない場合はエラーにせずデフォルト値を返す (ツールは設定ファイルが
// 無い状態でも動く必要がある)｡JSON の構文が壊れている場合や個々のキーの型/値が
// 不正な場合も､そのキーだけデフォルトへ落として起動を継続する｡
func Load(path string) (Config, error) {
	cfg := Default()

	data, err := os.ReadFile(path) // #nosec G304 -- 呼び出し元が固定パスを渡す想定
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return cfg, nil
		}
		return cfg, err
	}

	// Unmarshal が型不一致のフィールドをスキップして「できる範囲まで」埋めてくれるため､
	// ここで返るエラーは無視してよい (壊れたキーは raw 側で nil のまま残り､
	// 後段の applyRaw がデフォルトへ落とす)｡
	var raw rawConfig
	_ = json.Unmarshal(data, &raw)

	applyRaw(&cfg, &raw)
	return cfg, nil
}

// applyRaw は raw の各キーを検証し､妥当なものだけ cfg へ反映する｡
// applyRaw は raw の各キーを検証しつつ cfg へ反映する｡値が不正なキーは
// 黙って既定値のままにする (タイプミス 1 つで監視全体が止まらないようにするため)｡
//
// 意味のまとまりで 6 つに割ってある｡1 関数に並べると認知的複雑度が
// lint の閾値を超えるうえ､どのキーがどの役割かも読み取りにくくなるため｡
func applyRaw(cfg *Config, raw *rawConfig) {
	applyRawBasics(cfg, raw)
	applyRawDetection(cfg, raw)
	applyRawRecovery(cfg, raw)
	applyRawCompactStall(cfg, raw)
	applyRawCompactAuto(cfg, raw)
	applyRawCacheIdleCompact(cfg, raw)
}

// applyRawBasics は稼働そのものに関わる設定を反映する｡
func applyRawBasics(cfg *Config, raw *rawConfig) {
	if raw.Enabled != nil {
		cfg.Enabled = *raw.Enabled
	}
	if v := raw.PollIntervalSeconds; v != nil && *v >= 1 {
		cfg.PollIntervalSeconds = *v
	}
	if v := raw.MarginSeconds; v != nil && *v >= 0 {
		cfg.MarginSeconds = *v
	}
	if v := raw.FallbackWaitHours; v != nil && *v >= 0.1 {
		cfg.FallbackWaitHours = *v
	}
	if v := raw.MaxRetries; v != nil && *v >= 1 {
		cfg.MaxRetries = *v
	}
	if v := raw.IdleShutdownMinutes; v != nil && *v >= 1 {
		cfg.IdleShutdownMinutes = *v
	}
}

// applyRawDetection は「上限に当たったと判断してよいか」に関わる設定を反映する｡
func applyRawDetection(cfg *Config, raw *rawConfig) {
	if v := raw.ReadSource; v != nil && isValidReadSource(*v) {
		cfg.ReadSource = *v
	}
	if v := raw.ReadLines; v != nil && *v >= 5 {
		cfg.ReadLines = *v
	}
	if v := raw.DetectionTailLines; v != nil && *v >= 1 {
		cfg.DetectionTailLines = *v
	}
	if v := raw.UsedPercentageThreshold; v != nil && *v >= 0 && *v <= 100 {
		cfg.UsedPercentageThreshold = *v
	}
	if v := raw.StateMaxAgeSeconds; v != nil && *v >= 1 {
		cfg.StateMaxAgeSeconds = *v
	}
	if v := raw.CustomPatterns; v != nil {
		cfg.CustomPatterns = sanitizePatterns(*v)
	}
	if v := raw.CustomTransientPatterns; v != nil {
		cfg.CustomTransientPatterns = sanitizePatterns(*v)
	}
}

// applyRawRecovery は再開の送り方と一時エラーの扱いに関わる設定を反映する｡
func applyRawRecovery(cfg *Config, raw *rawConfig) {
	if v := raw.RetryMessage; v != nil && strings.TrimSpace(*v) != "" {
		cfg.RetryMessage = *v
	}
	if raw.HandleTransient != nil {
		cfg.HandleTransient = *raw.HandleTransient
	}
	if v := raw.TransientWaitSeconds; v != nil && *v >= 1 {
		cfg.TransientWaitSeconds = *v
	}
	if v := raw.TransientMaxWaitSeconds; v != nil && *v >= 1 {
		cfg.TransientMaxWaitSeconds = *v
	}
	// 上限が下限を下回る組み合わせは意味を成さないので引き上げる｡
	cfg.TransientMaxWaitSeconds = max(cfg.TransientMaxWaitSeconds, cfg.TransientWaitSeconds)
	if raw.DeferToNativeAutoContinue != nil {
		cfg.DeferToNativeAutoContinue = *raw.DeferToNativeAutoContinue
	}
	if raw.DismissMenu != nil {
		cfg.DismissMenu = *raw.DismissMenu
	}
	if v := raw.MenuDismissDelayMs; v != nil && *v >= 0 {
		cfg.MenuDismissDelayMs = *v
	}
	if v := raw.SubmitDelayMs; v != nil && *v >= 0 {
		cfg.SubmitDelayMs = *v
	}
	if v := raw.EngagedLabel; v != nil && strings.TrimSpace(*v) != "" {
		cfg.EngagedLabel = *v
	}
}

// applyRawCompactStall は compact 直後の停止を突く機能の設定を反映する｡
func applyRawCompactStall(cfg *Config, raw *rawConfig) {
	if raw.CompactStallEnabled != nil {
		cfg.CompactStallEnabled = *raw.CompactStallEnabled
	}
	// 画面が数秒変わらないだけで突くと､compact 直後に考え始めた Claude へ
	// 割り込む｡短すぎる値は受け付けない｡
	if v := raw.CompactStallQuietSeconds; v != nil && *v >= 10 {
		cfg.CompactStallQuietSeconds = *v
	}
	if v := raw.CompactStallCooldownMinutes; v != nil && *v >= 1 {
		cfg.CompactStallCooldownMinutes = *v
	}
	if v := raw.CompactStallMaxNudges; v != nil && *v >= 1 {
		cfg.CompactStallMaxNudges = *v
	}
	if v := raw.CompactStallMessage; v != nil && strings.TrimSpace(*v) != "" {
		cfg.CompactStallMessage = *v
	}
}

// applyRawCompactAuto は context 使用率からの自動 compact と､圧縮後の再開の設定を反映する｡
func applyRawCompactAuto(cfg *Config, raw *rawConfig) {
	if raw.CompactAutoEnabled != nil {
		cfg.CompactAutoEnabled = *raw.CompactAutoEnabled
	}
	if v := raw.CompactAutoThresholdPercent; v != nil && *v > 0 && *v <= 100 {
		cfg.CompactAutoThresholdPercent = *v
	}
	if v := raw.CompactAutoPrepMessage; v != nil && strings.TrimSpace(*v) != "" {
		cfg.CompactAutoPrepMessage = *v
	}
	if v := raw.CompactAutoMessage; v != nil && strings.TrimSpace(*v) != "" {
		cfg.CompactAutoMessage = *v
	}
	if v := raw.CompactAutoPrepTimeoutMinutes; v != nil && *v >= 1 {
		cfg.CompactAutoPrepTimeoutMinutes = *v
	}
	if v := raw.CompactAutoCooldownMinutes; v != nil && *v >= 1 {
		cfg.CompactAutoCooldownMinutes = *v
	}
	if raw.CompactResumeEnabled != nil {
		cfg.CompactResumeEnabled = *raw.CompactResumeEnabled
	}
	// 圧縮直後に人が続きを打つ余地を残す｡短すぎる値は受け付けない｡
	if v := raw.CompactResumeDelaySeconds; v != nil && *v >= 10 {
		cfg.CompactResumeDelaySeconds = *v
	}
}

// applyRawCacheIdleCompact は prompt cache の失効前の compact (idle compact) の設定を反映する｡
func applyRawCacheIdleCompact(cfg *Config, raw *rawConfig) {
	if raw.CacheIdleCompactEnabled != nil {
		cfg.CacheIdleCompactEnabled = *raw.CacheIdleCompactEnabled
	}
	// 短すぎると 2 段が失効に間に合わず､1 時間以上だと 1h TTL でも応答の直後から窓が開く｡
	if v := raw.CacheIdleCompactLeadSeconds; v != nil && *v >= 60 && *v < 3600 {
		cfg.CacheIdleCompactLeadSeconds = *v
	}
	if v := raw.CacheIdleCompactThresholdPercent; v != nil && *v > 0 && *v <= 100 {
		cfg.CacheIdleCompactThresholdPercent = *v
	}
}

func isValidReadSource(s string) bool {
	switch s {
	case ReadSourceVisible, ReadSourceRecent, ReadSourceRecentUnwrapped:
		return true
	default:
		return false
	}
}

// sanitizePatterns はコンパイルできない正規表現を除外する｡不正な 1 件のために
// 設定全体を破棄しない｡
func sanitizePatterns(patterns []string) []string {
	filtered := make([]string, 0, len(patterns))
	for _, p := range patterns {
		if _, err := regexp.Compile(p); err != nil {
			continue
		}
		filtered = append(filtered, p)
	}
	return filtered
}
