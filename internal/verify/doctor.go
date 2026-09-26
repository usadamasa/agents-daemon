// Package verify は `doctor` / `inspect` / `simulate` サブコマンドの実装を提供する｡
// いずれも daemon (internal/daemon) と同じ検知・判定経路 (monitor パッケージ) を
// 送信せずに覗き見る､または使い捨て pane に対してのみ実際に通す診断ツール｡
package verify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/usadamasa/agents-daemon/internal/apppath"
	"github.com/usadamasa/agents-daemon/internal/config"
	"github.com/usadamasa/agents-daemon/internal/daemon"
	"github.com/usadamasa/agents-daemon/internal/herdrcli"
	"github.com/usadamasa/agents-daemon/internal/sessionstate"
)

// RunDoctor は各前提条件を順に確認し w へ報告する｡herdr に到達できない場合だけ
// error を返す (呼び出し元の main が exit 1 にする)｡それ以外の欠落や陳腐化は
// 情報として報告するに留め､error にはしない｡
func RunDoctor(ctx context.Context, w io.Writer, client herdrcli.Client, p apppath.Paths, now time.Time) error {
	_, _ = fmt.Fprintln(w, "=== agents-daemon doctor ===")

	_, _ = fmt.Fprintln(w, "\n-- herdr 到達性 --")
	if err := client.Reachable(ctx); err != nil {
		_, _ = fmt.Fprintf(w, "  NG: herdr に到達できません: %v\n", err)
		return fmt.Errorf("herdr に到達できないため､これ以降の診断は行えません: %w", err)
	}
	_, _ = fmt.Fprintln(w, "  OK: herdr server は稼働しています")

	_, _ = fmt.Fprintln(w, "\n-- pane 一覧 --")
	panes, err := client.PaneList(ctx)
	if err != nil {
		// herdr 自体には到達できているのにここで失敗するのは異常｡診断ツールとして
		// 機能しない状態なので broken 扱いにする｡
		return fmt.Errorf("herdr pane list に失敗しました (herdr には到達できるのに異常): %w", err)
	}
	reportPanes(w, panes)

	_, _ = fmt.Fprintln(w, "\n-- statusline state (セッションごとの rate-limits) --")
	reportRateLimitStates(w, p, panes, now)

	_, _ = fmt.Fprintln(w, "\n-- 設定ファイル --")
	reportConfigState(w, p)

	_, _ = fmt.Fprintln(w, "\n-- daemon --")
	reportDaemonState(w, p, now)

	_, _ = fmt.Fprintln(w, "\n-- 解決済みパス --")
	reportPaths(w, p)

	return nil
}

// reportPanes は pane 一覧のうち Claude agent と判定されたものを列挙する｡
func reportPanes(w io.Writer, panes []herdrcli.Pane) {
	claudeCount := 0
	for _, pane := range panes {
		if !herdrcli.IsClaudeAgent(pane) {
			continue
		}
		claudeCount++
		sessionID := "(なし)"
		if pane.AgentSession != nil && pane.AgentSession.Value != "" {
			sessionID = pane.AgentSession.Value
		}
		_, _ = fmt.Fprintf(w, "  pane=%s cwd=%s agent_status=%s session_id=%s\n",
			pane.PaneID, pane.CWD, pane.AgentStatus, sessionID)
	}
	if claudeCount == 0 {
		_, _ = fmt.Fprintln(w, "  Claude agent の pane が見つかりません")
	}
	_, _ = fmt.Fprintf(w, "  pane 総数: %d 件 (うち Claude agent: %d 件)\n", len(panes), claudeCount)
}

// reportRateLimitState は statusline.sh が書き出す rate-limits.json の有無､鮮度､
// 5 時間ウィンドウの使用率を報告する｡ファイルが無いこと自体は異常ではない
// (statusline がまだ 1 度も書いていない)｡
func reportRateLimitStates(w io.Writer, p apppath.Paths, panes []herdrcli.Pane, now time.Time) {
	cfg, cfgErr := config.Load(p.ConfigFile())
	if cfgErr != nil {
		_, _ = fmt.Fprintf(w, "  設定の読み込みに失敗したため､既定の鮮度閾値で判定します: %v\n", cfgErr)
		cfg = config.Default()
	}

	found := false
	for _, pane := range panes {
		if !herdrcli.IsClaudeAgent(pane) {
			continue
		}
		found = true
		_, _ = fmt.Fprintf(w, "  pane=%s session_id=%s\n", pane.PaneID, defaultString(pane.SessionID(), "(なし)"))
		reportSessionRateLimitState(w, cfg, p, pane.SessionID(), now)
	}
	if !found {
		_, _ = fmt.Fprintln(w, "  Claude agent の pane が無いため､対応する state もありません")
	}
}

// defaultString は s が空のとき fallback を返す｡
func defaultString(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// reportSessionRateLimitState は 1 セッションぶんの state を報告する｡
func reportSessionRateLimitState(w io.Writer, cfg config.Config, p apppath.Paths, sessionID string, now time.Time) {
	state, err := sessionstate.New(p.StateDir()).LoadRateLimit(sessionID)
	if err != nil {
		_, _ = fmt.Fprintf(w, "  state ファイルの読み込みに失敗: %v\n", err)
		return
	}
	if state == nil {
		_, _ = fmt.Fprintln(w, "  state ファイルはまだありません (statusline がまだ rate_limits を書き出していません)")
		return
	}

	maxAge := time.Duration(cfg.StateMaxAgeSeconds) * time.Second
	observedAt := state.ObservedAt
	_, _ = fmt.Fprintf(w, "  observed_at: %s (%s 前)\n", observedAt.Format(time.RFC3339), now.Sub(observedAt).Round(time.Second))
	if state.IsFresh(now, maxAge) {
		_, _ = fmt.Fprintf(w, "  鮮度: OK (閾値 %s 以内)\n", maxAge)
	} else {
		_, _ = fmt.Fprintf(w, "  鮮度: 古い (閾値 %s を超過｡daemon はゲートを開いたまま画面テキストだけで判定します)\n", maxAge)
	}
	_, _ = fmt.Fprintf(w, "  five_hour.used_percentage: %.1f%% (閾値 %.1f%%)\n", state.FiveHourUsed, cfg.UsedPercentageThreshold)
	if state.FiveHourExhausted(cfg.UsedPercentageThreshold) {
		_, _ = fmt.Fprintln(w, "  → 閾値以上: limit 検知時にゲートで抑制しません")
	} else {
		_, _ = fmt.Fprintln(w, "  → 閾値未満: fresh な間は limit と判定されてもゲートで抑制されます")
	}
	_, _ = fmt.Fprintf(w, "  five_hour.resets_at: %s\n", state.FiveHourResetsAt.Format(time.RFC3339))
	// ファイル名と中身の session_id は必ず一致するはずなので､一致しているときは
	// 出さない｡食い違っていたら state の対応付けが壊れているということなので､
	// そのときだけ報告する｡
	if state.SessionID != "" && state.SessionID != sessionID {
		_, _ = fmt.Fprintf(w, "  ⚠ ファイル名と中身の session_id が食い違っています (中身: %s)\n", state.SessionID)
	}
}

// reportConfigState は設定ファイル (config.json) の有無と実効値を報告する｡config.Load は
// 個々のキーが不正でも黙ってデフォルトへ落とすため (起動を止めない方針)､ここでは
// 実効値がデフォルトと一致するかどうかをヒントとして添える｡一致していても偶然
// 同値のことがあり､逆に不一致でも意図した値のことがあるので､あくまで目安｡
func reportConfigState(w io.Writer, p apppath.Paths) {
	path := p.ConfigFile()
	data, statErr := os.ReadFile(path) // #nosec G304 -- 固定パス
	if statErr != nil {
		if errors.Is(statErr, os.ErrNotExist) {
			_, _ = fmt.Fprintf(w, "  %s は存在しません｡全項目デフォルト値で動作します\n", path)
			return
		}
		_, _ = fmt.Fprintf(w, "  %s の読み込みに失敗: %v\n", path, statErr)
		return
	}
	_, _ = fmt.Fprintf(w, "  %s は存在します\n", path)
	if !json.Valid(data) {
		_, _ = fmt.Fprintln(w, "  JSON として不正です｡config.Load() はこれを無視し､全項目デフォルト値にフォールバックします")
	}

	cfg, err := config.Load(path)
	if err != nil {
		_, _ = fmt.Fprintf(w, "  読み込みに失敗しました｡デフォルト値で継続します: %v\n", err)
		cfg = config.Default()
	}
	def := config.Default()
	_, _ = fmt.Fprintln(w, "  実効値 ((*)=デフォルトと異なる値｡ファイルの値が反映されている可能性が高い):")
	for _, f := range configFields(cfg, def) {
		_, _ = fmt.Fprintf(w, "    %-28s = %v%s\n", f.name, f.value, f.diffMark)
	}
}

// configFieldReport は 1 設定項目ぶんの実効値レポート行｡
type configFieldReport struct {
	name     string
	value    any
	diffMark string
}

// configFields は cfg の主要フィールドを名前付きで列挙し､def との差分マークを添える｡
func configFields(cfg, def config.Config) []configFieldReport {
	raw := []struct {
		name     string
		value    any
		defValue any
	}{
		{"enabled", cfg.Enabled, def.Enabled},
		{"pollIntervalSeconds", cfg.PollIntervalSeconds, def.PollIntervalSeconds},
		{"marginSeconds", cfg.MarginSeconds, def.MarginSeconds},
		{"fallbackWaitHours", cfg.FallbackWaitHours, def.FallbackWaitHours},
		{"maxRetries", cfg.MaxRetries, def.MaxRetries},
		{"retryMessage", cfg.RetryMessage, def.RetryMessage},
		{"readSource", cfg.ReadSource, def.ReadSource},
		{"readLines", cfg.ReadLines, def.ReadLines},
		{"detectionTailLines", cfg.DetectionTailLines, def.DetectionTailLines},
		{"usedPercentageThreshold", cfg.UsedPercentageThreshold, def.UsedPercentageThreshold},
		{"stateMaxAgeSeconds", cfg.StateMaxAgeSeconds, def.StateMaxAgeSeconds},
		{"handleTransient", cfg.HandleTransient, def.HandleTransient},
		{"transientWaitSeconds", cfg.TransientWaitSeconds, def.TransientWaitSeconds},
		{"transientMaxWaitSeconds", cfg.TransientMaxWaitSeconds, def.TransientMaxWaitSeconds},
		{"dismissMenu", cfg.DismissMenu, def.DismissMenu},
		{"menuDismissDelayMs", cfg.MenuDismissDelayMs, def.MenuDismissDelayMs},
		{"submitDelayMs", cfg.SubmitDelayMs, def.SubmitDelayMs},
		{"engagedLabel", cfg.EngagedLabel, def.EngagedLabel},
		{"customPatterns", cfg.CustomPatterns, def.CustomPatterns},
		{"customTransientPatterns", cfg.CustomTransientPatterns, def.CustomTransientPatterns},
		{"idleShutdownMinutes", cfg.IdleShutdownMinutes, def.IdleShutdownMinutes},
		{"deferToNativeAutoContinue", cfg.DeferToNativeAutoContinue, def.DeferToNativeAutoContinue},
		{"compactStallEnabled", cfg.CompactStallEnabled, def.CompactStallEnabled},
		{"compactStallQuietSeconds", cfg.CompactStallQuietSeconds, def.CompactStallQuietSeconds},
		{"compactStallCooldownMinutes", cfg.CompactStallCooldownMinutes, def.CompactStallCooldownMinutes},
		{"compactStallMaxNudges", cfg.CompactStallMaxNudges, def.CompactStallMaxNudges},
		{"compactStallMessage", cfg.CompactStallMessage, def.CompactStallMessage},
		{"compactAutoEnabled", cfg.CompactAutoEnabled, def.CompactAutoEnabled},
		{"compactAutoThresholdPercent", cfg.CompactAutoThresholdPercent, def.CompactAutoThresholdPercent},
		{"compactAutoPrepMessage", cfg.CompactAutoPrepMessage, def.CompactAutoPrepMessage},
		{"compactAutoMessage", cfg.CompactAutoMessage, def.CompactAutoMessage},
		{"compactAutoPrepTimeoutMinutes", cfg.CompactAutoPrepTimeoutMinutes, def.CompactAutoPrepTimeoutMinutes},
		{"compactAutoCooldownMinutes", cfg.CompactAutoCooldownMinutes, def.CompactAutoCooldownMinutes},
		{"compactResumeEnabled", cfg.CompactResumeEnabled, def.CompactResumeEnabled},
		{"compactResumeDelaySeconds", cfg.CompactResumeDelaySeconds, def.CompactResumeDelaySeconds},
	}
	out := make([]configFieldReport, 0, len(raw))
	for _, r := range raw {
		mark := ""
		if fmt.Sprintf("%v", r.value) != fmt.Sprintf("%v", r.defValue) {
			mark = " (*)"
		}
		out = append(out, configFieldReport{name: r.name, value: r.value, diffMark: mark})
	}
	return out
}

// reportDaemonState は PID ファイルと status.json から daemon の稼働状況を報告する｡
func reportDaemonState(w io.Writer, p apppath.Paths, now time.Time) {
	pid, err := daemon.ReadPIDFile(p.PIDFile())
	if err != nil {
		_, _ = fmt.Fprintf(w, "  PID ファイルの読み込みに失敗: %v\n", err)
		return
	}
	if pid == 0 || !daemon.PIDAlive(pid) {
		_, _ = fmt.Fprintln(w, "  daemon は起動していません")
		return
	}
	_, _ = fmt.Fprintf(w, "  daemon 起動中 (pid %d)\n", pid)

	snap, err := daemon.ReadStatusSnapshot(p.StatusFile())
	if err != nil {
		_, _ = fmt.Fprintf(w, "  status snapshot の読み込みに失敗: %v\n", err)
		return
	}
	if snap == nil {
		_, _ = fmt.Fprintln(w, "  status snapshot がまだありません (起動直後の可能性があります)")
		return
	}
	_, _ = fmt.Fprintf(w, "  最終 tick: %s (%s 前)\n", snap.UpdatedAt.Format(time.RFC3339), now.Sub(snap.UpdatedAt).Round(time.Second))
	_, _ = fmt.Fprintf(w, "  監視中 pane: %d 件\n", len(snap.Panes))
}

// reportPaths は daemon / CLI が触るランタイムファイルの解決済み絶対パスを列挙する｡
func reportPaths(w io.Writer, p apppath.Paths) {
	_, _ = fmt.Fprintf(w, "  config:      %s\n", p.ConfigFile())
	_, _ = fmt.Fprintf(w, "  state dir:   %s\n", p.StateDir())
	_, _ = fmt.Fprintf(w, "  rate limits: %s/<session_id>.json\n", sessionstate.New(p.StateDir()).RateLimits)
	_, _ = fmt.Fprintf(w, "  pid file:    %s\n", p.PIDFile())
	_, _ = fmt.Fprintf(w, "  status file: %s\n", p.StatusFile())
	_, _ = fmt.Fprintf(w, "  log file:    %s\n", p.LogFile())
}
