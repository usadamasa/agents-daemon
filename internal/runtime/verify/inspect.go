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
	"github.com/usadamasa/agents-daemon/internal/runtime/claudesessions"
	"github.com/usadamasa/agents-daemon/internal/runtime/config"
	"github.com/usadamasa/agents-daemon/internal/runtime/detect"
	"github.com/usadamasa/agents-daemon/internal/runtime/monitor"
	"github.com/usadamasa/agents-daemon/internal/transcript"
)

// PaneClient は inspect が --pane で生きた pane を読むときに使う操作。pane の情報 (record と session_id と pid) と画面テキスト
// だけで、送らない (#301)。--file のときは使わないので nil でよい。backend.Connect が返す multiplexer.Client (全部入り) を
// そのまま渡す。
type PaneClient interface {
	// PaneGet は指定 pane の情報を返す。
	PaneGet(ctx context.Context, paneID string) (multiplexer.Pane, error)
	// PaneRead は pane の画面テキストを返す。
	PaneRead(ctx context.Context, paneID string, opts multiplexer.PaneReadOptions) (string, error)
}

// InspectInput は inspect の入力。PaneID か File のどちらか一方を指定する (呼び出し元の cobra が検証する)。
type InspectInput struct {
	Client PaneClient
	Paths  apppath.Paths
	// ClaudeConfigDir は Claude Code の設定ディレクトリ (apppath.ClaudeConfigDir)。transcript と登録簿を読む。
	ClaudeConfigDir string
	// PaneID は rex 越しに読む block。File は保存済みの画面テキスト。
	PaneID string
	File   string
	// SessionID は transcript を引くセッション。--pane では pane から解決するので空でよい。
	SessionID string
	Now       time.Time
}

// InspectReport は inspect が集めた判定と根拠。文字の出力 (writeInspect) と `--json` の出力が同じ判定を見るように、
// 先にこれへ集めてから書く。
type InspectReport struct {
	Source      string `json:"source"`
	ConfigError string `json:"config_error,omitempty"`
	// Record と PaneError は --pane のときだけ。
	Record    string `json:"record,omitempty"`
	PaneError string `json:"pane_error,omitempty"`
	// SessionID と SessionSource (statusline / registry / flag) は transcript を引いたセッション。
	SessionID     string `json:"session_id,omitempty"`
	SessionSource string `json:"session_source,omitempty"`
	// Transcript は daemon が上限の判定に読む末尾の entry。Kind はその分類 (detect.Kind の String)。
	Transcript TranscriptReport `json:"transcript"`
	Kind       string           `json:"kind"`
	// Plan は daemon が監視中にこの entry をどう扱うか (monitor.Plan の ID。wait / weekly-far / stale-turn / hard-cap / none)。
	Plan string `json:"plan"`
	// Wake は kind が limit / weekly-limit のときの起床時刻と、その算出に使った情報源。weekly-far でも、近づいて待機に入った
	// ときの起床時刻として出す。
	Wake *WakeReport `json:"wake,omitempty"`
	// HandleTransient は kind が transient のときだけ、その扱いを決める設定。
	HandleTransient *bool `json:"handle_transient,omitempty"`
	// 画面の通知欄。Hint は detect.Hint の String。待機明けのキー選択にだけ効く。
	TotalLines         int      `json:"total_lines"`
	DetectionTailLines int      `json:"detection_tail_lines"`
	ScannedLines       []string `json:"scanned_lines"`
	Hint               string   `json:"hint"`
	// DeferToNativeAutoContinue は hint が native-armed のときだけ、その扱いを決める設定。
	DeferToNativeAutoContinue *bool `json:"defer_to_native_auto_continue,omitempty"`

	kind detect.Kind
	plan monitor.Plan
	hint detect.Hint
}

// TranscriptReport は transcript の末尾の読み取り。読めなければ Reason に理由。
type TranscriptReport struct {
	Path      string       `json:"path,omitempty"`
	Reason    string       `json:"reason,omitempty"`
	LastEntry *EntryReport `json:"last_entry,omitempty"`
}

// EntryReport は末尾の会話の entry のうち判定に効く欄。
type EntryReport struct {
	UUID           string       `json:"uuid"`
	Type           string       `json:"type"`
	Timestamp      time.Time    `json:"timestamp"`
	Version        string       `json:"version,omitempty"`
	Error          string       `json:"error,omitempty"`
	APIErrorStatus int          `json:"api_error_status,omitempty"`
	Quota          *QuotaReport `json:"quota_limits,omitempty"`
	// Text は本文の頭 (textHead 文字まで)。
	Text string `json:"text,omitempty"`
}

// QuotaReport は entry の quotaLimits のうち読んだ欄。
type QuotaReport struct {
	Status        string     `json:"status"`
	RateLimitType string     `json:"rate_limit_type,omitempty"`
	ResetsAt      *time.Time `json:"resets_at,omitempty"`
	OverageStatus string     `json:"overage_status,omitempty"`
}

// textHead は EntryReport.Text に残す本文の長さ (rune)。
const textHead = 200

// WakeReport は kind が limit / weekly-limit のときの起床時刻と、その算出に使った情報源。
type WakeReport struct {
	SourceID           string     `json:"source"`
	SourceDescription  string     `json:"source_description"`
	MarginSeconds      int        `json:"margin_seconds"`
	TranscriptResetsAt *time.Time `json:"transcript_resets_at,omitempty"`
	WakeAt             time.Time  `json:"wake_at"`
}

// RunInspect は transcript の末尾の分類と待機時刻の算出根拠、画面の通知欄の判定を w へ表示する。
func RunInspect(ctx context.Context, w io.Writer, in InspectInput) error {
	rep, err := CollectInspect(ctx, in)
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
// 画面を読めないときは error を返す。
func CollectInspect(ctx context.Context, in InspectInput) (InspectReport, error) {
	var rep InspectReport
	cfg, cfgErr := config.Load(in.Paths.ConfigFile())
	if cfgErr != nil {
		rep.ConfigError = cfgErr.Error()
		cfg = config.Default()
	}

	screen, source, err := loadInspectScreen(ctx, in.Client, cfg, in.PaneID, in.File)
	if err != nil {
		return rep, err
	}
	rep.Source = source
	rep.TotalLines, rep.ScannedLines = scannedLines(screen, cfg.DetectionTailLines)
	rep.DetectionTailLines = cfg.DetectionTailLines
	rep.hint = detect.ClassifyScreen(screen, cfg.DetectionTailLines)
	rep.Hint = rep.hint.String()
	if rep.hint == detect.HintNativeArmed {
		rep.DeferToNativeAutoContinue = &cfg.DeferToNativeAutoContinue
	}

	rep.SessionID, rep.SessionSource = in.SessionID, ""
	if in.SessionID != "" {
		rep.SessionSource = "flag"
	}
	if in.PaneID != "" {
		collectPaneSession(ctx, in, &rep)
	}

	// 上限かどうかは daemon と同じ経路 (transcript.Store.Tail、detect.ClassifyTurn) で決める。
	tail := transcript.New(in.ClaudeConfigDir).Tail(rep.SessionID)
	rep.Transcript = transcriptReport(tail)
	rep.kind = detect.ClassifyTurn(tail.Entry)
	rep.Kind = rep.kind.String()
	rep.plan = monitor.Decide(rep.kind, tail.Entry, in.Now)
	rep.Plan = rep.plan.ID()

	switch {
	case rep.kind.Windowed():
		wake := collectWake(cfg, tail.Entry, in.Now)
		rep.Wake = &wake
	case rep.kind == detect.KindTransient:
		rep.HandleTransient = &cfg.HandleTransient
	}
	return rep, nil
}

// collectPaneSession は --pane の record と、どのセッションの transcript を引くかを同じ応答から取る。
// session は daemon と同じ順で引く (statusline の記録、無ければ前景の pid で Claude Code の登録簿)。
func collectPaneSession(ctx context.Context, in InspectInput, rep *InspectReport) {
	pane, err := in.Client.PaneGet(ctx, in.PaneID)
	if err != nil {
		rep.PaneError = err.Error()
		return
	}
	rep.Record = multiplexer.RecordLabel(pane)
	if rep.SessionID != "" {
		return
	}
	if pane.SessionID != "" {
		rep.SessionID, rep.SessionSource = pane.SessionID, "statusline"
		return
	}
	if in.ClaudeConfigDir == "" || pane.PID == 0 {
		return
	}
	ids, err := claudesessions.New(in.ClaudeConfigDir).SessionIDs()
	if err != nil {
		rep.PaneError = err.Error()
		return
	}
	if sid := ids[pane.PID]; sid != "" {
		rep.SessionID, rep.SessionSource = sid, "registry"
	}
}

// transcriptReport は読んだ末尾を報告の形に写す。
func transcriptReport(tail transcript.Tail) TranscriptReport {
	rep := TranscriptReport{Path: tail.Path, Reason: tail.Reason}
	e := tail.Entry
	if e == nil {
		return rep
	}
	rep.LastEntry = &EntryReport{
		UUID: e.UUID, Type: e.Type, Timestamp: e.Timestamp, Version: e.Version,
		Error: e.Error, APIErrorStatus: e.APIErrorStatus, Text: headRunes(e.Text, textHead),
	}
	if q := e.Quota; q != nil {
		rep.LastEntry.Quota = &QuotaReport{Status: q.Status, RateLimitType: q.RateLimitType, OverageStatus: q.OverageStatus}
		if !q.ResetsAt.IsZero() {
			rep.LastEntry.Quota.ResetsAt = new(q.ResetsAt)
		}
	}
	return rep
}

// headRunes は s を先頭 n rune までに切る。
func headRunes(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n])
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
	if rep.PaneError != "" {
		_, _ = fmt.Fprintf(w, "pane の情報を取れません (record と session_id は無しとして続けます): %s\n", rep.PaneError)
	}
	if rep.Record != "" {
		_, _ = fmt.Fprintf(w, "record: %s\n", rep.Record)
	}
	if rep.SessionID == "" {
		_, _ = fmt.Fprintln(w, "対象セッション: 決まらない (--file のときは --session <id> を付けると transcript を引きます)")
	} else {
		_, _ = fmt.Fprintf(w, "対象セッション: %s (%s)\n", rep.SessionID, rep.SessionSource)
	}

	_, _ = fmt.Fprintln(w, "\n--- transcript の末尾 (上限かどうかはここで決まる) ---")
	writeTranscript(w, rep.Transcript)
	_, _ = fmt.Fprintf(w, "\n判定結果: %s\n", rep.Kind)
	_, _ = fmt.Fprintf(w, "daemon の扱い: %s\n", rep.plan)
	switch {
	case rep.kind.Windowed():
		_, _ = fmt.Fprintln(w, "\n--- 起床時刻の算出 ---")
		writeWake(w, *rep.Wake)
	case rep.kind == detect.KindTransient:
		_, _ = fmt.Fprintf(w, "\ntransient エラーとして扱う設定 (handleTransient): %v\n", *rep.HandleTransient)
	}

	_, _ = fmt.Fprintf(w, "\n--- 画面の通知欄 (待機明けに送るキーの選択にだけ効く。走査対象: 末尾 %d 行 / 総行数 %d) ---\n", len(rep.ScannedLines), rep.TotalLines)
	for _, l := range rep.ScannedLines {
		_, _ = fmt.Fprintf(w, "  %s\n", l)
	}
	_, _ = fmt.Fprintf(w, "通知欄の判定: %s\n", rep.Hint)
	switch rep.hint {
	case detect.HintNone:
		_, _ = fmt.Fprintln(w, "  → 待機明けには esc → 文面 → enter を送ります")
	case detect.HintResumeReady:
		_, _ = fmt.Fprintln(w, "  → 解除済みで Enter 待ちなので Enter だけを送ります (テキストは打ちません)")
	case detect.HintNativeArmed:
		_, _ = fmt.Fprintf(w, "  → Claude Code 自身の auto-continue が解除を待っています。deferToNativeAutoContinue: %v", *rep.DeferToNativeAutoContinue)
		if *rep.DeferToNativeAutoContinue {
			_, _ = fmt.Fprintln(w, " → 手を出しません (Escape はネイティブの取り消しキー)")
		} else {
			_, _ = fmt.Fprintln(w, " → 通常の再開を送ります")
		}
	case detect.HintNativeRetrying:
		_, _ = fmt.Fprintln(w, "  → Claude Code が request の中でリトライ中なので手を出しません")
	case detect.HintLowerPriority:
		_, _ = fmt.Fprintln(w, "  → lower-priority mode で作業中なので手を出しません")
	}
}

// writeTranscript は末尾の entry とその欄を書く。
func writeTranscript(w io.Writer, rep TranscriptReport) {
	if rep.LastEntry == nil {
		_, _ = fmt.Fprintf(w, "  読めない: %s\n", rep.Reason)
		return
	}
	e := rep.LastEntry
	_, _ = fmt.Fprintf(w, "  path: %s\n", rep.Path)
	_, _ = fmt.Fprintf(w, "  entry: uuid=%s type=%s at %s (Claude Code %s)\n", e.UUID, e.Type, e.Timestamp.Format(time.RFC3339), e.Version)
	if e.Error != "" {
		_, _ = fmt.Fprintf(w, "  error: %s (status %d)\n", e.Error, e.APIErrorStatus)
	}
	if q := e.Quota; q != nil {
		resets := "無し"
		if q.ResetsAt != nil {
			resets = q.ResetsAt.Format(time.RFC3339)
		}
		_, _ = fmt.Fprintf(w, "  quotaLimits: status=%s rateLimitType=%s resetsAt=%s\n", q.Status, q.RateLimitType, resets)
	}
	if e.Text != "" {
		_, _ = fmt.Fprintf(w, "  text: %s\n", strings.ReplaceAll(e.Text, "\n", " "))
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

// scannedLines は画面の総行数と、ClassifyScreen が実際に走査した末尾 n 行を返す。
// 「何を根拠に判定したか」を目視で確認できるようにするための出力。
func scannedLines(screen string, tailN int) (total int, scanned []string) {
	lines := strings.Split(screen, "\n")
	scanned = lines
	if tailN > 0 && len(scanned) > tailN {
		scanned = scanned[len(scanned)-tailN:]
	}
	return len(lines), scanned
}

// collectWake は monitor.ComputeLimitWake の結果と、その判断に使った情報源を集める。
func collectWake(cfg config.Config, entry *transcript.Entry, now time.Time) WakeReport {
	// 計算そのものは daemon と同じ monitor.ComputeLimitWake に任せる。ここで
	// 計算を書き直すと、診断の説明が実際の挙動とずれて嘘をつくようになる。
	wake, source := monitor.ComputeLimitWake(cfg, entry, now)
	rep := WakeReport{SourceID: source.ID(), SourceDescription: source.String(), MarginSeconds: cfg.MarginSeconds, WakeAt: wake}
	if entry != nil && entry.Quota != nil && !entry.Quota.ResetsAt.IsZero() {
		rep.TranscriptResetsAt = new(entry.Quota.ResetsAt)
	}
	return rep
}

// writeWake は起床時刻とその情報源を書く。
func writeWake(w io.Writer, wake WakeReport) {
	margin := time.Duration(wake.MarginSeconds) * time.Second
	_, _ = fmt.Fprintf(w, "  出所: %s + margin %s\n", wake.SourceDescription, margin)
	if wake.TranscriptResetsAt != nil {
		_, _ = fmt.Fprintf(w, "  transcript の entry の resetsAt: %s\n", wake.TranscriptResetsAt.Format(time.RFC3339))
	}
	_, _ = fmt.Fprintf(w, "  起床時刻: %s\n", wake.WakeAt.Format(time.RFC3339))
}
