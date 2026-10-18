// Package verify は `doctor` / `inspect` サブコマンドの実装を提供する。
// いずれも daemon (internal/daemon) と同じ検知・判定経路 (monitor パッケージ) を
// 送信せずに覗き見る診断ツール。使い捨て pane へ実際に通す `simulate` は internal/simulate。
package verify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/usadamasa/agents-daemon/internal/apppath"
	"github.com/usadamasa/agents-daemon/internal/config"
	"github.com/usadamasa/agents-daemon/internal/daemon"
	"github.com/usadamasa/agents-daemon/internal/domain"
	"github.com/usadamasa/agents-daemon/internal/multiplexer"
	"github.com/usadamasa/agents-daemon/internal/ratelimit"
)

// DoctorInput は RunDoctor が診断に使う環境。
type DoctorInput struct {
	Client multiplexer.Client
	Kind   string // Client の多重化ツール (backend.Detect の結果)
	Paths  apppath.Paths
	Now    time.Time
	Getenv func(string) string // peer messaging と rex CLI の前提 (REX_BLOCK / REX_SESSION) に使う環境 (通常は os.Getenv)
	// Premises は rex CLI の前提の点検。Client がその口を持たなければ nil。
	Premises multiplexer.PremiseChecker
}

// RunDoctor は各前提条件を順に確認し w へ報告する。
// 多重化ツールの server に到達できない場合だけ error を返す (呼び出し元の
// main が exit 1 にする)。それ以外の欠落や陳腐化は情報として報告するに留め、error にはしない。
func RunDoctor(ctx context.Context, w io.Writer, in DoctorInput) error {
	_, _ = fmt.Fprintln(w, "=== agents-daemon doctor ===")

	_, _ = fmt.Fprintln(w, "\n-- backend --")
	_, _ = fmt.Fprintf(w, "  backend: %s\n", backendLabel(in.Kind))
	if err := in.Client.Reachable(ctx); err != nil {
		_, _ = fmt.Fprintf(w, "  NG: %s に到達できません: %v\n", in.Kind, err)
		return fmt.Errorf("%s に到達できないため、これ以降の診断は行えません: %w", in.Kind, err)
	}
	_, _ = fmt.Fprintf(w, "  OK: %s server は稼働しています\n", in.Kind)

	_, _ = fmt.Fprintln(w, "\n-- pane 一覧 --")
	panes, err := in.Client.PaneList(ctx)
	if err != nil {
		// server には到達できているのにここで失敗するのは異常。診断ツールとして
		// 機能しない状態なので broken 扱いにする。
		return fmt.Errorf("%s の pane 一覧の取得に失敗しました (%s には到達できるのに異常): %w", in.Kind, in.Kind, err)
	}
	reportPanes(w, panes)

	_, _ = fmt.Fprintf(w, "\n-- %s CLI の前提 (自分の block への固定コマンドの応答の形) --\n", in.Kind)
	reportPremises(ctx, w, in.Premises, in.Getenv)

	_, _ = fmt.Fprintln(w, "\n-- statusline state (セッションごとの rate-limits) --")
	reportRateLimitStates(w, in.Paths, panes, in.Now)

	_, _ = fmt.Fprintln(w, "\n-- 設定ファイル --")
	reportConfigState(w, in.Paths)

	_, _ = fmt.Fprintln(w, "\n-- domain (私用と業務の境界) --")
	reportDomain(w, in.Paths, panes, in.Getenv("HOME"))

	_, _ = fmt.Fprintln(w, "\n-- daemon --")
	reportDaemonState(w, in.Paths, in.Now)

	_, _ = fmt.Fprintln(w, "\n-- peer messaging (ListAgents / SendMessage) --")
	reportPeerMessaging(w, in.Getenv)

	_, _ = fmt.Fprintln(w, "\n-- 解決済みパス --")
	reportPaths(w, in.Paths)

	return nil
}

// backendLabel は backend と、それを選んだ根拠の環境変数。
func backendLabel(kind string) string {
	return kind + " (REX_SESSION あり)"
}

// reportPanes は pane 一覧のうち Claude agent と判定されたものを列挙する。
func reportPanes(w io.Writer, panes []multiplexer.Pane) {
	claudeCount := 0
	for _, pane := range panes {
		if !multiplexer.IsClaudeAgent(pane) {
			continue
		}
		claudeCount++
		// RecordLabel は agent_status= から始まり、kind と msg はあるときだけ続く。
		_, _ = fmt.Fprintf(w, "  pane=%s cwd=%s %s session_id=%s\n",
			pane.PaneID, pane.CWD, multiplexer.RecordLabel(pane), defaultString(pane.SessionID, "(なし)"))
		if pane.UnknownReason != "" {
			_, _ = fmt.Fprintf(w, "    unknown の理由: %s\n", pane.UnknownReason)
		}
	}
	if claudeCount == 0 {
		_, _ = fmt.Fprintln(w, "  Claude agent の pane が見つかりません")
	}
	_, _ = fmt.Fprintf(w, "  pane 総数: %d 件 (うち Claude agent: %d 件)\n", len(panes), claudeCount)
}

// reportPremises は rex CLI の応答の形が internal/rexcli の読む形のままかを、自分の block (REX_BLOCK) に
// 固定のコマンドを打って報告する。ずれは ⚠ で出すだけで doctor の exit code には響かない
// (rex を上げた直後に全 pane が unknown になる原因を、changelog を読む前に名指しで見るため)。
func reportPremises(ctx context.Context, w io.Writer, checker multiplexer.PremiseChecker, getenv func(string) string) {
	if checker == nil {
		_, _ = fmt.Fprintln(w, "  この backend には点検の口が無いため省きます")
		return
	}
	blockID := getenv("REX_BLOCK")
	if blockID == "" {
		_, _ = fmt.Fprintln(w, "  REX_BLOCK が無いため点検しません (rex の block の中の Bash ツールから呼ぶと自分の block で点検します)")
		return
	}
	for _, f := range checker.CheckPremises(ctx, blockID, getenv("REX_SESSION")) {
		if f.OK {
			_, _ = fmt.Fprintf(w, "  OK: %s\n", f.Text)
		} else {
			_, _ = fmt.Fprintf(w, "  ⚠ %s\n", f.Text)
		}
	}
}

// reportRateLimitStates は ingest-statusline が書き出す rate-limits.json の有無、鮮度、
// 5 時間ウィンドウの使用率を報告する。ファイルが無いこと自体は異常ではない
// (statusline から ingest-statusline がまだ 1 度も rate_limits を受け取っていない)。
func reportRateLimitStates(w io.Writer, p apppath.Paths, panes []multiplexer.Pane, now time.Time) {
	cfg, cfgErr := config.Load(p.ConfigFile())
	if cfgErr != nil {
		_, _ = fmt.Fprintf(w, "  設定の読み込みに失敗したため、既定の鮮度閾値で判定します: %v\n", cfgErr)
		cfg = config.Default()
	}

	found := false
	for _, pane := range panes {
		if !multiplexer.IsClaudeAgent(pane) {
			continue
		}
		found = true
		_, _ = fmt.Fprintf(w, "  pane=%s session_id=%s\n", pane.PaneID, defaultString(pane.SessionID, "(なし)"))
		reportSessionRateLimitState(w, cfg, p, pane.SessionID, now)
	}
	if !found {
		_, _ = fmt.Fprintln(w, "  Claude agent の pane が無いため、対応する state もありません")
	}
}

// defaultString は s が空のとき fallback を返す。
func defaultString(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// reportSessionRateLimitState は 1 セッションぶんの state を報告する。
func reportSessionRateLimitState(w io.Writer, cfg config.Config, p apppath.Paths, sessionID string, now time.Time) {
	state, err := ratelimit.New(p.StateDir()).Load(sessionID)
	if err != nil {
		_, _ = fmt.Fprintf(w, "  state ファイルの読み込みに失敗: %v\n", err)
		return
	}
	if state == nil {
		_, _ = fmt.Fprintln(w, "  state ファイルはまだありません (statusline 経由の ingest-statusline がまだ rate_limits を受け取っていません)")
		return
	}

	maxAge := time.Duration(cfg.StateMaxAgeSeconds) * time.Second
	observedAt := state.ObservedAt
	_, _ = fmt.Fprintf(w, "  observed_at: %s (%s 前)\n", observedAt.Format(time.RFC3339), now.Sub(observedAt).Round(time.Second))
	if state.IsFresh(now, maxAge) {
		_, _ = fmt.Fprintf(w, "  鮮度: OK (閾値 %s 以内)\n", maxAge)
	} else {
		_, _ = fmt.Fprintf(w, "  鮮度: 古い (閾値 %s を超過。daemon はゲートを開いたまま画面テキストだけで判定します)\n", maxAge)
	}
	_, _ = fmt.Fprintf(w, "  five_hour.used_percentage: %.1f%% (閾値 %.1f%%)\n", state.FiveHourUsed, cfg.UsedPercentageThreshold)
	if state.FiveHourExhausted(cfg.UsedPercentageThreshold) {
		_, _ = fmt.Fprintln(w, "  → 閾値以上: limit 検知時にゲートで抑制しません")
	} else {
		_, _ = fmt.Fprintln(w, "  → 閾値未満: fresh な間は limit と判定されてもゲートで抑制されます")
	}
	_, _ = fmt.Fprintf(w, "  five_hour.resets_at: %s\n", state.FiveHourResetsAt.Format(time.RFC3339))
	// ファイル名と中身の session_id は必ず一致するはずなので、一致しているときは
	// 出さない。食い違っていたら state の対応付けが壊れているということなので、
	// そのときだけ報告する。
	if state.SessionID != "" && state.SessionID != sessionID {
		_, _ = fmt.Fprintf(w, "  ⚠ ファイル名と中身の session_id が食い違っています (中身: %s)\n", state.SessionID)
	}
}

// reportConfigState は設定ファイル (config.json) の有無と実効値を報告する。config.Load は
// 個々のキーが不正でも黙ってデフォルトへ落とすため (起動を止めない方針)、ここでは
// 実効値がデフォルトと一致するかどうかをヒントとして添える。一致していても偶然
// 同値のことがあり、逆に不一致でも意図した値のことがあるので、あくまで目安。
func reportConfigState(w io.Writer, p apppath.Paths) {
	path := p.ConfigFile()
	data, statErr := os.ReadFile(path) // #nosec G304 -- 固定パス
	if statErr != nil {
		if errors.Is(statErr, os.ErrNotExist) {
			_, _ = fmt.Fprintf(w, "  %s は存在しません。全項目デフォルト値で動作します\n", path)
			return
		}
		_, _ = fmt.Fprintf(w, "  %s の読み込みに失敗: %v\n", path, statErr)
		return
	}
	_, _ = fmt.Fprintf(w, "  %s は存在します\n", path)
	if !json.Valid(data) {
		_, _ = fmt.Fprintln(w, "  JSON として不正です。config.Load() はこれを無視し、全項目デフォルト値にフォールバックします")
	}

	cfg, err := config.Load(path)
	if err != nil {
		_, _ = fmt.Fprintf(w, "  読み込みに失敗しました。デフォルト値で継続します: %v\n", err)
		cfg = config.Default()
	}
	def := config.Default()
	_, _ = fmt.Fprintln(w, "  実効値 ((*)=デフォルトと異なる値。ファイルの値が反映されている可能性が高い):")
	for _, f := range configFields(cfg, def) {
		_, _ = fmt.Fprintf(w, "    %-28s = %v%s\n", f.name, f.value, f.diffMark)
	}
}

// configFieldReport は 1 設定項目ぶんの実効値レポート行。
type configFieldReport struct {
	name     string
	value    any
	diffMark string
}

// configFields は cfg の主要フィールドを名前付きで列挙し、def との差分マークを添える。
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
		{"customPatterns", cfg.CustomPatterns, def.CustomPatterns},
		{"idleShutdownMinutes", cfg.IdleShutdownMinutes, def.IdleShutdownMinutes},
		{"deferToNativeAutoContinue", cfg.DeferToNativeAutoContinue, def.DeferToNativeAutoContinue},
		{"homePaths", cfg.HomePaths, def.HomePaths},
		{"defaultDomain", cfg.DefaultDomain, def.DefaultDomain},
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

// reportDomain は config の homePaths と defaultDomain から、Claude agent の pane の cwd を
// 私用と業務のどちらに判定するかを報告する。判定を誤ると業務のものが私用の側に混ざるので、
// 設定を書いたら pane ごとの結果をここで確かめる。
func reportDomain(w io.Writer, p apppath.Paths, panes []multiplexer.Pane, home string) {
	cfg, err := config.Load(p.ConfigFile())
	if err != nil {
		_, _ = fmt.Fprintf(w, "  設定の読み込みに失敗したため、既定 (すべて業務) で判定します: %v\n", err)
	}
	c := domain.New(home, cfg.HomePaths, cfg.DefaultDomain)
	_, _ = fmt.Fprintf(w, "  homePaths に当たらない cwd: %s\n", c.Fallback())
	if paths := c.HomePaths(); len(paths) > 0 {
		_, _ = fmt.Fprintf(w, "  私用と判定するパス: %s\n", strings.Join(paths, ", "))
	} else if c.Fallback() == domain.Work {
		_, _ = fmt.Fprintln(w, "  私用と判定するパスはありません (私用の端末なら defaultDomain を home にする)")
	}
	if dropped := len(cfg.HomePaths) - len(c.HomePaths()); dropped > 0 {
		_, _ = fmt.Fprintf(w, "  ⚠ homePaths のうち %d 件は絶対パスでも ~/ 始まりでもないため無視しています\n", dropped)
	}
	for _, pane := range panes {
		if !multiplexer.IsClaudeAgent(pane) {
			continue
		}
		_, _ = fmt.Fprintf(w, "  pane=%s %s cwd=%s\n", pane.PaneID, c.Classify(pane.CWD), pane.CWD)
	}
}

// reportDaemonState は PID ファイルと status.json から daemon の稼働状況を報告する。
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

// reportPeerMessaging は Claude Code の cross-session messaging がこのプロセスの環境で
// 使える状態かを報告する。Claude Code は各セッションで CLAUDE_CODE_MESSAGING_SOCKET を
// 子プロセス (Bash ツール) へ渡すので、doctor をセッション内から呼んだときだけ判定できる。
// 設定側の切り分け (gate を止める env、crossSessionInbound、deny) は setup skill の
// references/peer-messaging.md が持ち、ここは観測できる事実だけを出す。
func reportPeerMessaging(w io.Writer, getenv func(string) string) {
	socket := getenv("CLAUDE_CODE_MESSAGING_SOCKET")
	if socket == "" {
		_, _ = fmt.Fprintln(w, "  CLAUDE_CODE_MESSAGING_SOCKET: (なし)")
		_, _ = fmt.Fprintln(w, "  Claude Code のセッションの外から呼んだか、そのセッションで cross-session messaging が無効。"+
			"セッション内 (Bash ツール) から呼んで (なし) なら setup skill の references/peer-messaging.md で切り分ける")
		return
	}
	_, _ = fmt.Fprintf(w, "  CLAUDE_CODE_MESSAGING_SOCKET: %s\n", socket)
	if _, err := os.Stat(socket); err != nil {
		_, _ = fmt.Fprintf(w, "  NG: socket がありません (bind に失敗したか、セッションが終了している): %v\n", err)
	} else {
		_, _ = fmt.Fprintln(w, "  OK: socket があります")
	}
	// sandbox 化した子プロセスとして起動した Claude Code は CLAUDE_CODE_TMPDIR を受け取り、
	// socket 先が /tmp/claude-<uid>/cc-socks/ へ移って seatbelt に bind を拒否される。
	// 変数そのものは親セッションの Bash ツールにも来る (sandbox が渡す) ので、変数の有無ではなく
	// socket のパスがその下に居るかで入れ子を見分ける。
	if tmpdir := getenv("CLAUDE_CODE_TMPDIR"); tmpdir != "" && isUnder(socket, tmpdir) {
		_, _ = fmt.Fprintf(w, "  ⚠ socket が CLAUDE_CODE_TMPDIR (%s) の下にあります。入れ子で起動したセッション (sandbox 内の claude) では bind できず、messaging は使えません\n", tmpdir)
	}
}

// isUnder は path が dir 配下にあるかをパスの区切りで判定する (接頭辞の文字列一致では
// /tmp/claude-501 と /tmp/claude-5010 を区別できない)。
func isUnder(path, dir string) bool {
	rel, err := filepath.Rel(filepath.Clean(dir), filepath.Clean(path))
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, "../") && !filepath.IsAbs(rel)
}

// reportPaths は daemon / CLI が触るランタイムファイルの解決済み絶対パスを列挙する。
func reportPaths(w io.Writer, p apppath.Paths) {
	_, _ = fmt.Fprintf(w, "  config:      %s\n", p.ConfigFile())
	_, _ = fmt.Fprintf(w, "  state dir:   %s\n", p.StateDir())
	_, _ = fmt.Fprintf(w, "  rate limits: %s/<session_id>.json\n", ratelimit.New(p.StateDir()).Dir)
	_, _ = fmt.Fprintf(w, "  pid file:    %s\n", p.PIDFile())
	_, _ = fmt.Fprintf(w, "  status file: %s\n", p.StatusFile())
	_, _ = fmt.Fprintf(w, "  log file:    %s\n", p.LogFile())
}
