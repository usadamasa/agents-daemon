// Package config は agents-daemon の設定 (${XDG_CONFIG_HOME:-~/.config}/agents-daemon/config.json) を
// 読み込み、検証する。ファイルが存在しない場合は全項目デフォルトとして扱い、
// 個々のキーが不正な場合もそのキーだけデフォルトへ落として起動を継続する。
// 設定ファイルの typo で監視全体が黙って無効化される事故を避けるための方針である。
package config

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
)

// defaultRetryMessage は上限解除後の再開で pane へ送る文面。上限の検知語 (limit / rate 等) を
// 含めてはいけない (自分の送信を自分で検知して再送を繰り返す)。
const defaultRetryMessage = "Continue where you left off."

// Config は agents-daemon の検証済み設定。
type Config struct {
	Enabled             bool
	PollIntervalSeconds int
	MarginSeconds       int
	FallbackWaitHours   float64
	MaxRetries          int
	// RetryMessage は resume 時にペインへ送るテキスト。入力行へエコーされるため、
	// limit / rate / overloaded のような検知語を含めてはいけない。含めると
	// 監視が自分自身の送信を再検知して無限ループする。
	RetryMessage string
	// ReadLines は待機明けに pane の画面を末尾から何行読むか。通知欄の判定は更に末尾 DetectionTailLines 行だけを
	// 見るので、それ以上あれば判定は変わらない。上限かどうかは画面ではなく transcript で決める (detect)。
	ReadLines               int
	DetectionTailLines      int
	HandleTransient         bool
	TransientWaitSeconds    int
	TransientMaxWaitSeconds int
	DismissMenu             bool
	MenuDismissDelayMs      int
	SubmitDelayMs           int
	IdleShutdownMinutes     int
	// DeferToNativeAutoContinue が true のとき、Claude Code 自身の auto-continue
	// (2.1.234 の autoContinueAtUsageLimit) がカウントダウン中の pane には手を出さない。
	// この状態では Escape が取り消しキーなので、送信するとネイティブの継続を潰す。
	DeferToNativeAutoContinue bool
}

// Default は全項目デフォルト値の Config を返す。
func Default() Config {
	return Config{
		Enabled:                 true,
		PollIntervalSeconds:     5,
		MarginSeconds:           60,
		FallbackWaitHours:       5,
		MaxRetries:              5,
		RetryMessage:            defaultRetryMessage,
		ReadLines:               25,
		DetectionTailLines:      15,
		HandleTransient:         true,
		TransientWaitSeconds:    60,
		TransientMaxWaitSeconds: 300,
		DismissMenu:             true,
		MenuDismissDelayMs:      300,
		SubmitDelayMs:           400,
		IdleShutdownMinutes:     30,

		DeferToNativeAutoContinue: true,
	}
}

// rawConfig は設定ファイルをそのまま受ける入れ物。フィールドをポインタにしているのは
// 「キーが省略された」(nil のまま) と「キーは書かれているが値が不正」(非 nil だが
// validate で弾かれる) を区別するため。bool のように 0 値が意味を持つ型では
// 非ポインタだと「省略」と「明示的な false/0」を取り違えてしまう。
type rawConfig struct {
	Enabled                 *bool    `json:"enabled"`
	PollIntervalSeconds     *int     `json:"pollIntervalSeconds"`
	MarginSeconds           *int     `json:"marginSeconds"`
	FallbackWaitHours       *float64 `json:"fallbackWaitHours"`
	MaxRetries              *int     `json:"maxRetries"`
	RetryMessage            *string  `json:"retryMessage"`
	ReadLines               *int     `json:"readLines"`
	DetectionTailLines      *int     `json:"detectionTailLines"`
	HandleTransient         *bool    `json:"handleTransient"`
	TransientWaitSeconds    *int     `json:"transientWaitSeconds"`
	TransientMaxWaitSeconds *int     `json:"transientMaxWaitSeconds"`
	DismissMenu             *bool    `json:"dismissMenu"`
	MenuDismissDelayMs      *int     `json:"menuDismissDelayMs"`
	SubmitDelayMs           *int     `json:"submitDelayMs"`
	IdleShutdownMinutes     *int     `json:"idleShutdownMinutes"`

	DeferToNativeAutoContinue *bool `json:"deferToNativeAutoContinue"`
}

// Load は path から設定を読み込む。
// ファイルが存在しない場合はエラーにせずデフォルト値を返す (ツールは設定ファイルが
// 無い状態でも動く必要がある)。JSON の構文が壊れている場合や個々のキーの型/値が
// 不正な場合も、そのキーだけデフォルトへ落として起動を継続する。
func Load(path string) (Config, error) {
	cfg := Default()

	data, err := os.ReadFile(path) // #nosec G304 -- 呼び出し元が固定パスを渡す想定
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return cfg, nil
		}
		return cfg, err
	}

	// Unmarshal が型不一致のフィールドをスキップして「できる範囲まで」埋めてくれるため、
	// ここで返るエラーは無視してよい (壊れたキーは raw 側で nil のまま残り、
	// 後段の applyRaw がデフォルトへ落とす)。
	var raw rawConfig
	_ = json.Unmarshal(data, &raw)

	applyRaw(&cfg, &raw)
	return cfg, nil
}

// applyRaw は raw の各キーを検証し、妥当なものだけ cfg へ反映する。
// applyRaw は raw の各キーを検証しつつ cfg へ反映する。値が不正なキーは
// 黙って既定値のままにする (タイプミス 1 つで監視全体が止まらないようにするため)。
//
// 意味のまとまりで 3 つに割ってある。1 関数に並べると認知的複雑度が
// lint の閾値を超えるうえ、どのキーがどの役割かも読み取りにくくなるため。
func applyRaw(cfg *Config, raw *rawConfig) {
	applyRawBasics(cfg, raw)
	applyRawDetection(cfg, raw)
	applyRawRecovery(cfg, raw)
}

// applyRawBasics は稼働そのものに関わる設定を反映する。
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

// applyRawDetection は待機明けの画面の読み方 (通知欄の判定) に関わる設定を反映する。
func applyRawDetection(cfg *Config, raw *rawConfig) {
	if v := raw.ReadLines; v != nil && *v >= 5 {
		cfg.ReadLines = *v
	}
	if v := raw.DetectionTailLines; v != nil && *v >= 1 {
		cfg.DetectionTailLines = *v
	}
}

// applyRawRecovery は再開の送り方と一時エラーの扱いに関わる設定を反映する。
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
	// 上限が下限を下回る組み合わせは意味を成さないので引き上げる。
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
}
