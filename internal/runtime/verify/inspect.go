// Package verify は `inspect` / `status` / `logs` サブコマンドの実装を提供する。
// いずれも daemon パッケージと同じ検知・判定経路 (monitor パッケージ) を
// 送信せずに覗き見る診断ツール。前提条件の診断 `doctor` は doctor パッケージ、
// 使い捨て pane へ実際に通す `simulate` は simulate パッケージ。
//
// status は結果を StatusReport に集めてから書く。文字の出力と `--json` の出力が同じ判定を見るためで、
// JSON の形はその struct の json tag が決める。
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

// RunInspect は screen の分類と待機時刻の算出根拠を w へ表示する。--pane / --file の
// どちらか一方を指定する必要があるという制約は呼び出し元 (cobra の RunE) が検証する。
func RunInspect(ctx context.Context, w io.Writer, client PaneClient, p apppath.Paths, paneID, file, sessionID string, now time.Time) error {
	cfg, cfgErr := config.Load(p.ConfigFile())
	if cfgErr != nil {
		_, _ = fmt.Fprintf(w, "設定の読み込みに失敗、デフォルト値で継続します: %v\n", cfgErr)
		cfg = config.Default()
	}

	screen, source, err := loadInspectScreen(ctx, client, cfg, paneID, file)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(w, "読み取り元: %s\n", source)

	reportScannedLines(w, screen, cfg.DetectionTailLines)

	classifier, err := detect.NewClassifier(cfg.CustomPatterns)
	if err != nil {
		return fmt.Errorf("分類器の構築に失敗: %w", err)
	}
	kind := classifier.Classify(screen, cfg.DetectionTailLines)
	_, _ = fmt.Fprintf(w, "\n判定結果: %s\n", kind)

	if paneID != "" {
		// record (Claude Code の自己申告) と、どのセッションの利用上限 state を引くかを同じ応答から取る。
		// 上限で止まった pane の record が何かは未実測で (#77)、ここで見た値が記録になる。
		if pane, getErr := client.PaneGet(ctx, paneID); getErr != nil {
			_, _ = fmt.Fprintf(w, "pane の情報を取れません (record と session_id は無しとして続けます): %v\n", getErr)
		} else {
			_, _ = fmt.Fprintf(w, "record: %s\n", multiplexer.RecordLabel(pane))
			if sessionID == "" {
				sessionID = pane.SessionID
			}
		}
	}
	if sessionID == "" {
		_, _ = fmt.Fprintln(w, "\n対象セッションが決まらないため、利用上限 state によるゲート判定は行いません")
		_, _ = fmt.Fprintln(w, "  (--file を使うときは --session <id> でセッションを指定してください)")
	}
	store := ratelimit.New(p.StateDir())
	rateState, stateErr := store.Load(sessionID)
	if stateErr != nil {
		_, _ = fmt.Fprintf(w, "利用上限 state の読み込みに失敗、state 無しとして扱います: %v\n", stateErr)
		rateState = nil
	}

	account, accountErr := store.LatestWindow(now)
	if accountErr != nil {
		_, _ = fmt.Fprintf(w, "アカウント全体の利用上限 state の走査に失敗、無しとして扱います: %v\n", accountErr)
		account = nil
	}

	_, _ = fmt.Fprintln(w, "\n--- ゲート判定 (statusline 由来の 5 時間ウィンドウ使用率) ---")
	reportGate(w, cfg, rateState, now)

	switch kind {
	case detect.KindLimit:
		_, _ = fmt.Fprintln(w, "\n--- 起床時刻の算出 ---")
		reportWake(w, cfg, rateState, account, screen, now)
	case detect.KindTransient:
		_, _ = fmt.Fprintf(w, "\ntransient エラーとして扱う設定 (handleTransient): %v\n", cfg.HandleTransient)
	case detect.KindAutoContinueArmed:
		_, _ = fmt.Fprintln(w, "\nClaude Code 自身の auto-continue が解除を待っています。")
		_, _ = fmt.Fprintf(w, "  deferToNativeAutoContinue: %v", cfg.DeferToNativeAutoContinue)
		if cfg.DeferToNativeAutoContinue {
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

	return nil
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

// reportScannedLines は Classify が実際に走査した末尾 n 行を表示する。
// 「何を根拠に判定したか」を目視で確認できるようにするための出力。
func reportScannedLines(w io.Writer, screen string, tailN int) {
	lines := strings.Split(screen, "\n")
	scanned := lines
	if tailN > 0 && len(scanned) > tailN {
		scanned = scanned[len(scanned)-tailN:]
	}
	_, _ = fmt.Fprintf(w, "画面の総行数: %d 行 / 走査対象 (detectionTailLines=%d, 0 は全行): %d 行\n", len(lines), tailN, len(scanned))
	_, _ = fmt.Fprintln(w, "--- 走査対象 (末尾) ---")
	for _, l := range scanned {
		_, _ = fmt.Fprintf(w, "  %s\n", l)
	}
}

// reportGate はゲートの判定と、その根拠になった値を報告する。
//
// 判定そのものは monitor.LimitGate に任せる (reportWake が
// monitor.ComputeLimitWake を呼ぶのと同じ理由 — ここで判定を書き直すと、診断の
// 説明が実際の挙動とずれて嘘をつくようになる)。ここが持つのは、判定の内訳を
// どの順で見せるかという表示の都合だけ。
func reportGate(w io.Writer, cfg config.Config, rateState *ratelimit.State, now time.Time) {
	gate := monitor.LimitGate(cfg, rateState, now)
	if gate == monitor.GateNoState || gate == monitor.GateNoFiveHour {
		_, _ = fmt.Fprintf(w, "  %s\n", gate)
		return
	}

	maxAge := time.Duration(cfg.StateMaxAgeSeconds) * time.Second
	_, _ = fmt.Fprintf(w, "  observed_at からの経過: %s (閾値 %s)\n", now.Sub(rateState.ObservedAt).Round(time.Second), maxAge)
	if gate == monitor.GateStale {
		_, _ = fmt.Fprintf(w, "  %s\n", gate)
		return
	}

	_, _ = fmt.Fprintf(w, "  five_hour.used_percentage: %.1f%% / 閾値 %.1f%%\n", rateState.FiveHourUsed, cfg.UsedPercentageThreshold)
	_, _ = fmt.Fprintf(w, "  → %s\n", gate)
}

// reportWake は monitor.computeLimitWake と同じ優先順位を表示用に再現する。
// (reportGate と同じ理由で、書き込みを伴わない独立した再現に留まる。)
func reportWake(w io.Writer, cfg config.Config, rateState, account *ratelimit.State, screen string, now time.Time) {
	// 計算そのものは daemon と同じ monitor.ComputeLimitWake に任せる。ここで
	// 計算を書き直すと、診断の説明が実際の挙動とずれて嘘をつくようになる。
	wake, source := monitor.ComputeLimitWake(cfg, rateState, account, screen, now)

	margin := time.Duration(cfg.MarginSeconds) * time.Second
	_, _ = fmt.Fprintf(w, "  出所: %s + margin %s\n", source, margin)
	if rateState != nil && rateState.HasFiveHour() {
		_, _ = fmt.Fprintf(w, "  自セッションの state の resets_at: %s\n", rateState.FiveHourResetsAt.Format(time.RFC3339))
	} else {
		_, _ = fmt.Fprintln(w, "  自セッションの state: 無し (上限中に開いた新規セッション、または five_hour が null)")
	}
	if at, ok := detect.ParseAbsoluteReset(screen, cfg.DetectionTailLines, now, time.Local); ok {
		_, _ = fmt.Fprintf(w, "  画面の絶対時刻: %s\n", at.Format(time.RFC3339))
	} else {
		_, _ = fmt.Fprintln(w, "  画面の絶対時刻: 読めない")
	}
	if account != nil && account.HasFiveHour() {
		_, _ = fmt.Fprintf(w, "  アカウント全体で最新のウィンドウ: resets_at %s (session %s が %s に観測)\n",
			account.FiveHourResetsAt.Format(time.RFC3339), account.SessionID,
			account.ObservedAt.Format(time.RFC3339))
	} else {
		_, _ = fmt.Fprintln(w, "  アカウント全体で最新のウィンドウ: 無し")
	}
	_, _ = fmt.Fprintf(w, "  起床時刻: %s\n", wake.Format(time.RFC3339))
}
