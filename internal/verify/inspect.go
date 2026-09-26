package verify

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/usadamasa/agents-daemon/internal/apppath"
	"github.com/usadamasa/agents-daemon/internal/config"
	"github.com/usadamasa/agents-daemon/internal/detect"
	"github.com/usadamasa/agents-daemon/internal/herdrcli"
	"github.com/usadamasa/agents-daemon/internal/monitor"
	"github.com/usadamasa/agents-daemon/internal/sessionstate"
)

// RunInspect は screen の分類と待機時刻の算出根拠を w へ表示する｡--pane / --file の
// どちらか一方を指定する必要があるという制約は呼び出し元 (cobra の RunE) が検証する｡
func RunInspect(ctx context.Context, w io.Writer, client herdrcli.Client, p apppath.Paths, paneID, file, sessionID string, now time.Time) error {
	cfg, cfgErr := config.Load(p.ConfigFile())
	if cfgErr != nil {
		_, _ = fmt.Fprintf(w, "設定の読み込みに失敗､デフォルト値で継続します: %v\n", cfgErr)
		cfg = config.Default()
	}

	screen, source, err := loadInspectScreen(ctx, client, cfg, paneID, file)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(w, "読み取り元: %s\n", source)

	reportScannedLines(w, screen, cfg.DetectionTailLines)

	classifier, err := detect.NewClassifier(cfg.CustomPatterns, cfg.CustomTransientPatterns)
	if err != nil {
		return fmt.Errorf("分類器の構築に失敗: %w", err)
	}
	kind := classifier.Classify(screen, cfg.DetectionTailLines)
	_, _ = fmt.Fprintf(w, "\n判定結果: %s\n", kind)

	if sessionID == "" {
		sessionID = paneSessionID(ctx, client, paneID)
	}
	if sessionID == "" {
		_, _ = fmt.Fprintln(w, "\n対象セッションが決まらないため､利用上限 state によるゲート判定は行いません")
		_, _ = fmt.Fprintln(w, "  (--file を使うときは --session <id> でセッションを指定してください)")
	}
	store := sessionstate.New(p.StateDir())
	rateState, stateErr := store.LoadRateLimit(sessionID)
	if stateErr != nil {
		_, _ = fmt.Fprintf(w, "利用上限 state の読み込みに失敗､state 無しとして扱います: %v\n", stateErr)
		rateState = nil
	}

	account, accountErr := store.LatestWindow(now)
	if accountErr != nil {
		_, _ = fmt.Fprintf(w, "アカウント全体の利用上限 state の走査に失敗､無しとして扱います: %v\n", accountErr)
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
		_, _ = fmt.Fprintln(w, "\nClaude Code 自身の auto-continue が解除を待っています｡")
		_, _ = fmt.Fprintf(w, "  deferToNativeAutoContinue: %v", cfg.DeferToNativeAutoContinue)
		if cfg.DeferToNativeAutoContinue {
			_, _ = fmt.Fprintln(w, " → 手を出しません (Escape はネイティブの取り消しキーなので送ると継続を潰します)")
		} else {
			_, _ = fmt.Fprintln(w, " → 通常の上限として自前で待機します")
		}
	case detect.KindResumeReady:
		_, _ = fmt.Fprintln(w, "\n上限は解除済みで Enter 待ちです｡Enter だけを送ります (テキストは打ちません)｡")
	case detect.KindSpendLimit:
		_, _ = fmt.Fprintln(w, "\n待っても解除されない上限 (spend limit / usage credit) です｡待機に入りません｡")
	case detect.KindNone:
		_, _ = fmt.Fprintln(w, "\n--- compact の自動化の判定 ---")
		reportCompactAuto(w, cfg, p, sessionID, screen, now)
		_, _ = fmt.Fprintln(w, "\n--- compact 停止の判定 (画面判定の fallback) ---")
		reportCompactStall(w, cfg, screen)
	}

	return nil
}

// reportCompactAuto は context 使用率からの自動 compact と､圧縮後の再開の判定を表示する｡
// agent_status は pane を引き直さないと分からないため､残りの条件として並べるに留める｡
func reportCompactAuto(w io.Writer, cfg config.Config, p apppath.Paths, sessionID, screen string, now time.Time) {
	if sessionID == "" {
		_, _ = fmt.Fprintln(w, "  対象セッションが決まらないため判定しません")
		return
	}
	store := sessionstate.New(p.StateDir())
	cs, err := store.LoadCompact(sessionID)
	if err != nil {
		_, _ = fmt.Fprintf(w, "  compact state の読み込みに失敗: %v\n", err)
		return
	}

	prompt := "書きかけがあります (送信しません)"
	if detect.PromptIsEmpty(screen, cfg.DetectionTailLines) {
		prompt = "空 (送信できます)"
	}
	_, _ = fmt.Fprintf(w, "  入力欄: %s\n", prompt)

	if !cs.HasContext {
		_, _ = fmt.Fprintf(w, "  context 使用率: state が無い (statusline が %s へ書きます)\n", store.Context)
	} else {
		maxAge := time.Duration(cfg.StateMaxAgeSeconds) * time.Second
		_, _ = fmt.Fprintf(w, "  context 使用率: %.0f%% / 閾値 %.0f%% (観測から %s､鮮度の上限 %s)\n",
			cs.UsedPercentage, cfg.CompactAutoThresholdPercent,
			now.Sub(cs.ObservedAt).Round(time.Second), maxAge)
	}
	if !cfg.CompactAutoEnabled {
		_, _ = fmt.Fprintln(w, "  compactAutoEnabled が false のため /compact-prep は投入しません")
	}

	if cs.HasPrep {
		_, _ = fmt.Fprintf(w, "  compact-prep の state file: %s に書かれています\n", cs.PrepWrittenAt.Format(time.RFC3339))
	} else {
		_, _ = fmt.Fprintln(w, "  compact-prep の state file: ありません")
	}

	if cs.HasCompacted {
		delay := time.Duration(cfg.CompactResumeDelaySeconds) * time.Second
		_, _ = fmt.Fprintf(w, "  圧縮完了 marker: %s (経過 %s / 猶予 %s)\n",
			cs.CompactedAt.Format(time.RFC3339), now.Sub(cs.CompactedAt).Round(time.Second), delay)
	} else {
		_, _ = fmt.Fprintln(w, "  圧縮完了 marker: ありません")
	}
	if !cfg.CompactResumeEnabled {
		_, _ = fmt.Fprintln(w, "  compactResumeEnabled が false のため再開は促しません")
	}
	_, _ = fmt.Fprintln(w, "  送信にはさらに agent_status が idle/done であることが要ります")
}

// reportCompactStall は compact 直後に止まっているかの候補判定を表示する｡
// 実際に送るかどうかは monitor が agent_status・画面の安定・クールダウンを
// 重ねて決めるため､ここでは「候補かどうか」と残りの条件を並べるに留める｡
func reportCompactStall(w io.Writer, cfg config.Config, screen string) {
	if !cfg.CompactStallEnabled {
		_, _ = fmt.Fprintln(w, "  compactStallEnabled が false のため判定しません")
		return
	}
	signature, ok := detect.CompactStallSignature(screen, cfg.DetectionTailLines)
	if !ok {
		_, _ = fmt.Fprintln(w, "  候補ではありません (compact 境界が無い / 境界の後ろに応答がある / 入力行が空でない)")
		return
	}
	_, _ = fmt.Fprintln(w, "  候補です｡根拠にした範囲:")
	for _, line := range strings.Split(signature, "\n") {
		_, _ = fmt.Fprintf(w, "    %s\n", line)
	}
	_, _ = fmt.Fprintf(w, "  送信にはさらに次が要ります: agent_status が idle/done､この画面が %d 秒変わらない､直近の送信から %d 分､同じ停止への送信が %d 回未満\n",
		cfg.CompactStallQuietSeconds, cfg.CompactStallCooldownMinutes, cfg.CompactStallMaxNudges)
}

// loadInspectScreen は --pane / --file のどちらかから画面テキストを取得する｡
func loadInspectScreen(ctx context.Context, client herdrcli.Client, cfg config.Config, paneID, file string) (screen, source string, err error) {
	if file != "" {
		data, readErr := os.ReadFile(file) // #nosec G304 -- CLI 引数で明示指定されたファイル
		if readErr != nil {
			return "", "", fmt.Errorf("ファイルの読み込みに失敗: %w", readErr)
		}
		return string(data), fmt.Sprintf("file:%s", file), nil
	}

	screen, err = client.PaneRead(ctx, paneID, herdrcli.PaneReadOptions{
		Source: herdrcli.PaneReadSource(cfg.ReadSource),
		Lines:  cfg.ReadLines,
	})
	if err != nil {
		return "", "", fmt.Errorf("pane %s の読み取りに失敗: %w", paneID, err)
	}
	return screen, fmt.Sprintf("pane:%s (source=%s lines=%d)", paneID, cfg.ReadSource, cfg.ReadLines), nil
}

// reportScannedLines は Classify が実際に走査した末尾 n 行を表示する｡
// 「何を根拠に判定したか」を目視で確認できるようにするための出力｡
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

// reportGate はゲートの判定と､その根拠になった値を報告する｡
//
// 判定そのものは monitor.LimitGate に任せる (reportWake が
// monitor.ComputeLimitWake を呼ぶのと同じ理由 — ここで判定を書き直すと､診断の
// 説明が実際の挙動とずれて嘘をつくようになる)｡ここが持つのは､判定の内訳を
// どの順で見せるかという表示の都合だけ｡
func reportGate(w io.Writer, cfg config.Config, rateState *sessionstate.RateLimit, now time.Time) {
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

// reportWake は monitor.computeLimitWake と同じ優先順位を表示用に再現する｡
// (reportGate と同じ理由で､書き込みを伴わない独立した再現に留まる｡)
func reportWake(w io.Writer, cfg config.Config, rateState, account *sessionstate.RateLimit, screen string, now time.Time) {
	// 計算そのものは daemon と同じ monitor.ComputeLimitWake に任せる｡ここで
	// 計算を書き直すと､診断の説明が実際の挙動とずれて嘘をつくようになる
	// (実際､以前は inspect 側だけ鮮度チェックが残っていて別の結果を出していた)｡
	wake, source := monitor.ComputeLimitWake(cfg, rateState, account, screen, now)

	margin := time.Duration(cfg.MarginSeconds) * time.Second
	_, _ = fmt.Fprintf(w, "  出所: %s + margin %s\n", source, margin)
	if rateState != nil && rateState.HasFiveHour() {
		_, _ = fmt.Fprintf(w, "  自セッションの state の resets_at: %s\n", rateState.FiveHourResetsAt.Format(time.RFC3339))
	} else {
		_, _ = fmt.Fprintln(w, "  自セッションの state: 無し (上限中に開いた新規セッション､または five_hour が null)")
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

// paneSessionID は pane に紐づく Claude セッション ID を引く｡pane が取得できない､
// または agent session が紐づいていない場合は空文字列を返す (state 無しとして扱う)｡
// 利用上限 state はセッションごとに別ファイルなので､この対応付けが取れないと
// どの state を読むべきかが決まらない｡
func paneSessionID(ctx context.Context, client herdrcli.Client, paneID string) string {
	if paneID == "" {
		return ""
	}
	pane, err := client.PaneGet(ctx, paneID)
	if err != nil {
		return ""
	}
	return pane.SessionID()
}
