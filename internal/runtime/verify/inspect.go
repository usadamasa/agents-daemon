// Package verify は `inspect` / `status` / `logs` サブコマンドの実装を提供する。
// いずれも daemon パッケージと同じ検知・判定経路 (monitor パッケージ) を
// 送信せずに覗き見る診断ツール。前提条件の診断 `doctor` は doctor パッケージ、
// 使い捨て pane へ実際に通す `simulate` は simulate パッケージ。
//
// どれも結果を struct (StatusReport、InspectReport、LogsReport) に集めてから書く。文字の出力と `--json` の出力が
// 同じ判定を見るためで、JSON の形はその struct の json tag が決める。
package verify

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/usadamasa/agents-daemon/internal/apppath"
	"github.com/usadamasa/agents-daemon/internal/multiplexer"
	"github.com/usadamasa/agents-daemon/internal/ratelimit"
	"github.com/usadamasa/agents-daemon/internal/runtime/config"
	"github.com/usadamasa/agents-daemon/internal/runtime/detect"
	"github.com/usadamasa/agents-daemon/internal/runtime/monitor"
)

// PaneClient は inspect が --pane で生きた pane を読むときに使う操作。pane の情報 (record と session_id) と画面テキスト
// だけで、送らない (#301)。--file のときは使わないので nil でよい。backend.Connect が返す multiplexer.Client (全部入り) を
// そのまま渡す。
type PaneClient interface {
	// PaneGet は指定 pane の情報を返す。
	PaneGet(ctx context.Context, paneID string) (multiplexer.Pane, error)
	// PaneRead は pane の画面テキストを返す。
	PaneRead(ctx context.Context, paneID string, opts multiplexer.PaneReadOptions) (string, error)
}

// InspectReport は inspect が集めた判定と根拠。文字の出力 (writeInspect) と `--json` の出力が同じ判定を見るように、
// 先にこれへ集めてから書く。
type InspectReport struct {
	Source             string   `json:"source"`
	ConfigError        string   `json:"config_error,omitempty"`
	TotalLines         int      `json:"total_lines"`
	DetectionTailLines int      `json:"detection_tail_lines"`
	ScannedLines       []string `json:"scanned_lines"`
	Kind               string   `json:"kind"`
	// Record と PaneError は --pane のときだけ。
	Record       string      `json:"record,omitempty"`
	PaneError    string      `json:"pane_error,omitempty"`
	SessionID    string      `json:"session_id,omitempty"`
	StateError   string      `json:"state_error,omitempty"`
	AccountError string      `json:"account_error,omitempty"`
	Gate         GateReport  `json:"gate"`
	Wake         *WakeReport `json:"wake,omitempty"`
	// 次の 2 つは kind が transient / auto-continue-armed のときだけ、その kind の扱いを決める設定。
	HandleTransient           *bool `json:"handle_transient,omitempty"`
	DeferToNativeAutoContinue *bool `json:"defer_to_native_auto_continue,omitempty"`

	kind detect.Kind
}

// GateReport はゲートの判定と、その根拠になった値。state が無い (no-state、no-five-hour) ときは ID と説明だけ。
type GateReport struct {
	ID            string   `json:"id"`
	Description   string   `json:"description"`
	AgeSeconds    *int64   `json:"age_seconds,omitempty"`
	MaxAgeSeconds int      `json:"max_age_seconds"`
	FiveHourUsed  *float64 `json:"five_hour_used_percentage,omitempty"`
	Threshold     float64  `json:"used_percentage_threshold"`

	gate monitor.Gate
}

// WakeReport は kind が limit のときの起床時刻と、その算出に使った情報源。
type WakeReport struct {
	SourceID          string     `json:"source"`
	SourceDescription string     `json:"source_description"`
	MarginSeconds     int        `json:"margin_seconds"`
	StateResetsAt     *time.Time `json:"state_resets_at,omitempty"`
	ScreenResetsAt    *time.Time `json:"screen_resets_at,omitempty"`
	AccountResetsAt   *time.Time `json:"account_resets_at,omitempty"`
	AccountSessionID  string     `json:"account_session_id,omitempty"`
	AccountObservedAt *time.Time `json:"account_observed_at,omitempty"`
	WakeAt            time.Time  `json:"wake_at"`
}

// RunInspect は screen の分類と待機時刻の算出根拠を w へ表示する。--pane / --file の
// どちらか一方を指定する必要があるという制約は呼び出し元 (cobra の RunE) が検証する。
func RunInspect(ctx context.Context, w io.Writer, client PaneClient, p apppath.Paths, paneID, file, sessionID string, now time.Time) error {
	rep, err := CollectInspect(ctx, client, p, paneID, file, sessionID, now)
	if err != nil {
		if rep.ConfigError != "" {
			writeConfigError(w, rep.ConfigError)
		}
		return err
	}
	writeInspect(w, rep)
	return nil
}

// CollectInspect は RunInspect と同じ判定を InspectReport に集める。`inspect --json` はこれをそのまま出す。
// 画面を読めないときと分類器を作れないときは error を返す。
func CollectInspect(ctx context.Context, client PaneClient, p apppath.Paths, paneID, file, sessionID string, now time.Time) (InspectReport, error) {
	var rep InspectReport
	cfg, cfgErr := config.Load(p.ConfigFile())
	if cfgErr != nil {
		rep.ConfigError = cfgErr.Error()
		cfg = config.Default()
	}

	screen, source, err := loadInspectScreen(ctx, client, cfg, paneID, file)
	if err != nil {
		return rep, err
	}
	rep.Source = source
	rep.TotalLines, rep.ScannedLines = scannedLines(screen, cfg.DetectionTailLines)
	rep.DetectionTailLines = cfg.DetectionTailLines

	classifier, err := detect.NewClassifier(cfg.CustomPatterns)
	if err != nil {
		return rep, fmt.Errorf("分類器の構築に失敗: %w", err)
	}
	rep.kind = classifier.Classify(screen, cfg.DetectionTailLines)
	rep.Kind = rep.kind.String()

	if paneID != "" {
		// record (Claude Code の自己申告) と、どのセッションの利用上限 state を引くかを同じ応答から取る。
		// 上限で止まった pane の record が何かは未実測で (#77)、ここで見た値が記録になる。
		if pane, getErr := client.PaneGet(ctx, paneID); getErr != nil {
			rep.PaneError = getErr.Error()
		} else {
			rep.Record = multiplexer.RecordLabel(pane)
			if sessionID == "" {
				sessionID = pane.SessionID
			}
		}
	}
	rep.SessionID = sessionID
	store := ratelimit.New(p.StateDir())
	rateState, stateErr := store.Load(sessionID)
	if stateErr != nil {
		rep.StateError = stateErr.Error()
		rateState = nil
	}
	account, accountErr := store.LatestWindow(now)
	if accountErr != nil {
		rep.AccountError = accountErr.Error()
		account = nil
	}

	rep.Gate = collectGate(cfg, rateState, now)
	switch rep.kind {
	case detect.KindLimit:
		wake := collectWake(cfg, rateState, account, screen, now)
		rep.Wake = &wake
	case detect.KindTransient:
		rep.HandleTransient = &cfg.HandleTransient
	case detect.KindAutoContinueArmed:
		rep.DeferToNativeAutoContinue = &cfg.DeferToNativeAutoContinue
	}
	return rep, nil
}

func writeConfigError(w io.Writer, msg string) {
	_, _ = fmt.Fprintf(w, "設定の読み込みに失敗、デフォルト値で継続します: %s\n", msg)
}

// writeInspect は InspectReport を人が読む形で w に書く。
func writeInspect(w io.Writer, rep InspectReport) {
	if rep.ConfigError != "" {
		writeConfigError(w, rep.ConfigError)
	}
	_, _ = fmt.Fprintf(w, "読み取り元: %s\n", rep.Source)
	_, _ = fmt.Fprintf(w, "画面の総行数: %d 行 / 走査対象 (detectionTailLines=%d, 0 は全行): %d 行\n", rep.TotalLines, rep.DetectionTailLines, len(rep.ScannedLines))
	_, _ = fmt.Fprintln(w, "--- 走査対象 (末尾) ---")
	for _, l := range rep.ScannedLines {
		_, _ = fmt.Fprintf(w, "  %s\n", l)
	}
	_, _ = fmt.Fprintf(w, "\n判定結果: %s\n", rep.Kind)

	if rep.PaneError != "" {
		_, _ = fmt.Fprintf(w, "pane の情報を取れません (record と session_id は無しとして続けます): %s\n", rep.PaneError)
	}
	if rep.Record != "" {
		_, _ = fmt.Fprintf(w, "record: %s\n", rep.Record)
	}
	if rep.SessionID == "" {
		_, _ = fmt.Fprintln(w, "\n対象セッションが決まらないため、利用上限 state によるゲート判定は行いません")
		_, _ = fmt.Fprintln(w, "  (--file を使うときは --session <id> でセッションを指定してください)")
	}
	if rep.StateError != "" {
		_, _ = fmt.Fprintf(w, "利用上限 state の読み込みに失敗、state 無しとして扱います: %s\n", rep.StateError)
	}
	if rep.AccountError != "" {
		_, _ = fmt.Fprintf(w, "アカウント全体の利用上限 state の走査に失敗、無しとして扱います: %s\n", rep.AccountError)
	}

	_, _ = fmt.Fprintln(w, "\n--- ゲート判定 (statusline 由来の 5 時間ウィンドウ使用率) ---")
	writeGate(w, rep.Gate)

	switch rep.kind {
	case detect.KindLimit:
		_, _ = fmt.Fprintln(w, "\n--- 起床時刻の算出 ---")
		writeWake(w, *rep.Wake)
	case detect.KindTransient:
		_, _ = fmt.Fprintf(w, "\ntransient エラーとして扱う設定 (handleTransient): %v\n", *rep.HandleTransient)
	case detect.KindAutoContinueArmed:
		deferToNative := *rep.DeferToNativeAutoContinue
		_, _ = fmt.Fprintln(w, "\nClaude Code 自身の auto-continue が解除を待っています。")
		_, _ = fmt.Fprintf(w, "  deferToNativeAutoContinue: %v", deferToNative)
		if deferToNative {
			_, _ = fmt.Fprintln(w, " → 手を出しません (Escape はネイティブの取り消しキーなので送ると継続を潰します)")
		} else {
			_, _ = fmt.Fprintln(w, " → 通常の上限として自前で待機します")
		}
	case detect.KindResumeReady:
		_, _ = fmt.Fprintln(w, "\n上限は解除済みで Enter 待ちです。Enter だけを送ります (テキストは打ちません)。")
	case detect.KindSpendLimit:
		_, _ = fmt.Fprintln(w, "\n待っても解除されない上限 (spend limit / usage credit) です。待機に入りません。")
	case detect.KindNone:
		_, _ = fmt.Fprintln(w, "\n上限でも一時エラーでもない画面です。daemon は何もしません。")
	}
}

// loadInspectScreen は --pane / --file のどちらかから画面テキストを取得する。
func loadInspectScreen(ctx context.Context, client PaneClient, cfg config.Config, paneID, file string) (screen, source string, err error) {
	if file != "" {
		data, readErr := os.ReadFile(file) // #nosec G304 -- CLI 引数で明示指定されたファイル
		if readErr != nil {
			return "", "", fmt.Errorf("ファイルの読み込みに失敗: %w", readErr)
		}
		return string(data), fmt.Sprintf("file:%s", file), nil
	}

	screen, err = client.PaneRead(ctx, paneID, multiplexer.PaneReadOptions{Lines: cfg.ReadLines})
	if err != nil {
		return "", "", fmt.Errorf("pane %s の読み取りに失敗: %w", paneID, err)
	}
	return screen, fmt.Sprintf("pane:%s (lines=%d)", paneID, cfg.ReadLines), nil
}

// scannedLines は画面の総行数と、Classify が実際に走査した末尾 n 行を返す。
// 「何を根拠に判定したか」を目視で確認できるようにするための出力。
func scannedLines(screen string, tailN int) (total int, scanned []string) {
	lines := strings.Split(screen, "\n")
	scanned = lines
	if tailN > 0 && len(scanned) > tailN {
		scanned = scanned[len(scanned)-tailN:]
	}
	return len(lines), scanned
}

// collectGate はゲートの判定と、その根拠になった値を集める。
//
// 判定そのものは monitor.LimitGate に任せる (collectWake が
// monitor.ComputeLimitWake を呼ぶのと同じ理由 — ここで判定を書き直すと、診断の
// 説明が実際の挙動とずれて嘘をつくようになる)。
func collectGate(cfg config.Config, rateState *ratelimit.State, now time.Time) GateReport {
	gate := monitor.LimitGate(cfg, rateState, now)
	rep := GateReport{
		ID: gate.ID(), Description: gate.String(), gate: gate,
		MaxAgeSeconds: cfg.StateMaxAgeSeconds, Threshold: cfg.UsedPercentageThreshold,
	}
	if gate == monitor.GateNoState || gate == monitor.GateNoFiveHour {
		return rep
	}
	age := int64(now.Sub(rateState.ObservedAt).Round(time.Second) / time.Second)
	rep.AgeSeconds = &age
	if gate != monitor.GateStale {
		used := rateState.FiveHourUsed
		rep.FiveHourUsed = &used
	}
	return rep
}

// writeGate はゲートの判定の内訳を、根拠の値から順に書く。
func writeGate(w io.Writer, g GateReport) {
	if g.AgeSeconds == nil {
		_, _ = fmt.Fprintf(w, "  %s\n", g.Description)
		return
	}
	maxAge := time.Duration(g.MaxAgeSeconds) * time.Second
	_, _ = fmt.Fprintf(w, "  observed_at からの経過: %s (閾値 %s)\n", time.Duration(*g.AgeSeconds)*time.Second, maxAge)
	if g.FiveHourUsed == nil {
		_, _ = fmt.Fprintf(w, "  %s\n", g.Description)
		return
	}
	_, _ = fmt.Fprintf(w, "  five_hour.used_percentage: %.1f%% / 閾値 %.1f%%\n", *g.FiveHourUsed, g.Threshold)
	_, _ = fmt.Fprintf(w, "  → %s\n", g.Description)
}

// collectWake は monitor.ComputeLimitWake の結果と、その優先順位の判断に使った情報源を集める。
func collectWake(cfg config.Config, rateState, account *ratelimit.State, screen string, now time.Time) WakeReport {
	// 計算そのものは daemon と同じ monitor.ComputeLimitWake に任せる。ここで
	// 計算を書き直すと、診断の説明が実際の挙動とずれて嘘をつくようになる。
	wake, source := monitor.ComputeLimitWake(cfg, rateState, account, screen, now)
	rep := WakeReport{SourceID: source.ID(), SourceDescription: source.String(), MarginSeconds: cfg.MarginSeconds, WakeAt: wake}
	if rateState != nil && rateState.HasFiveHour() {
		at := rateState.FiveHourResetsAt
		rep.StateResetsAt = &at
	}
	if at, ok := detect.ParseAbsoluteReset(screen, cfg.DetectionTailLines, now, time.Local); ok {
		rep.ScreenResetsAt = &at
	}
	if account != nil && account.HasFiveHour() {
		resets, observed := account.FiveHourResetsAt, account.ObservedAt
		rep.AccountResetsAt, rep.AccountSessionID, rep.AccountObservedAt = &resets, account.SessionID, &observed
	}
	return rep
}

// writeWake は起床時刻とその情報源を書く。
func writeWake(w io.Writer, wake WakeReport) {
	margin := time.Duration(wake.MarginSeconds) * time.Second
	_, _ = fmt.Fprintf(w, "  出所: %s + margin %s\n", wake.SourceDescription, margin)
	if wake.StateResetsAt != nil {
		_, _ = fmt.Fprintf(w, "  自セッションの state の resets_at: %s\n", wake.StateResetsAt.Format(time.RFC3339))
	} else {
		_, _ = fmt.Fprintln(w, "  自セッションの state: 無し (上限中に開いた新規セッション、または five_hour が null)")
	}
	if wake.ScreenResetsAt != nil {
		_, _ = fmt.Fprintf(w, "  画面の絶対時刻: %s\n", wake.ScreenResetsAt.Format(time.RFC3339))
	} else {
		_, _ = fmt.Fprintln(w, "  画面の絶対時刻: 読めない")
	}
	if wake.AccountResetsAt != nil {
		_, _ = fmt.Fprintf(w, "  アカウント全体で最新のウィンドウ: resets_at %s (session %s が %s に観測)\n",
			wake.AccountResetsAt.Format(time.RFC3339), wake.AccountSessionID,
			wake.AccountObservedAt.Format(time.RFC3339))
	} else {
		_, _ = fmt.Fprintln(w, "  アカウント全体で最新のウィンドウ: 無し")
	}
	_, _ = fmt.Fprintf(w, "  起床時刻: %s\n", wake.WakeAt.Format(time.RFC3339))
}
