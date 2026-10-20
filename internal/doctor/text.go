package doctor

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/usadamasa/agents-daemon/internal/detect"
	"github.com/usadamasa/agents-daemon/internal/domain"
)

// Write は Report を人が読む形で w へ書く。節の見出しと OK / NG / ⚠ の行の形は
// skill (agents-daemon:setup、catching-up-with-rex-changelog) が読む手がかりなので、変えるときはそちらも見る。
func Write(w io.Writer, rep Report) {
	_, _ = fmt.Fprintln(w, "=== agents-daemon doctor ===")

	_, _ = fmt.Fprintln(w, "\n-- backend --")
	writeBackend(w, rep.Backend)
	if rep.Panes == nil {
		return
	}

	_, _ = fmt.Fprintln(w, "\n-- pane 一覧 --")
	if rep.Panes.Error != "" {
		return
	}
	writePanes(w, *rep.Panes)

	_, _ = fmt.Fprintf(w, "\n-- %s CLI の前提 (自分の block への固定コマンドの応答の形) --\n", rep.Backend.Kind)
	writePremises(w, *rep.Premises)

	_, _ = fmt.Fprintln(w, "\n-- statusline state (セッションごとの rate-limits) --")
	writeRateLimitStates(w, *rep.RateLimits)

	_, _ = fmt.Fprintln(w, "\n-- 設定ファイル --")
	writeConfigState(w, *rep.Config)

	_, _ = fmt.Fprintln(w, "\n-- domain (私用と業務の境界) --")
	writeDomain(w, *rep.Domain)

	_, _ = fmt.Fprintln(w, "\n-- daemon --")
	writeDaemonState(w, *rep.Daemon)

	_, _ = fmt.Fprintln(w, "\n-- peer messaging (ListAgents / SendMessage) --")
	writePeerMessaging(w, *rep.PeerMessaging)

	_, _ = fmt.Fprintln(w, "\n-- 解決済みパス --")
	writePaths(w, *rep.Paths)
}

func writeBackend(w io.Writer, rep BackendReport) {
	_, _ = fmt.Fprintf(w, "  backend: %s (REX_SESSION あり)\n", rep.Kind)
	if !rep.Reachable {
		_, _ = fmt.Fprintf(w, "  NG: %s に到達できません: %s\n", rep.Kind, rep.Error)
		return
	}
	_, _ = fmt.Fprintf(w, "  OK: %s server は稼働しています\n", rep.Kind)
}

func writePanes(w io.Writer, rep PanesReport) {
	for _, pane := range rep.Claude {
		// recordLabel は agent_status= から始まり、kind と msg はあるときだけ続く。
		_, _ = fmt.Fprintf(w, "  pane=%s cwd=%s %s session_id=%s\n",
			pane.PaneID, pane.CWD, pane.recordLabel(), defaultString(pane.SessionID, "(なし)"))
		if pane.UnknownReason != "" {
			_, _ = fmt.Fprintf(w, "    unknown の理由: %s\n", pane.UnknownReason)
		}
		writePaneGeometry(w, pane)
	}
	if len(rep.Claude) == 0 {
		_, _ = fmt.Fprintln(w, "  Claude agent の pane が見つかりません")
	}
	_, _ = fmt.Fprintf(w, "  pane 総数: %d 件 (うち Claude agent: %d 件)\n", rep.Total, len(rep.Claude))
}

// writePaneGeometry は block の高さと screen を出し、高さが下限に満たなければ ⚠ を出す。
func writePaneGeometry(w io.Writer, pane PaneReport) {
	if pane.GeometryError != "" {
		_, _ = fmt.Fprintf(w, "    高さと screen を取れない: %s\n", pane.GeometryError)
		return
	}
	g := pane.Geometry
	_, _ = fmt.Fprintf(w, "    rows=%d columns=%d screen=%s\n", g.Rows, g.Columns, g.Screen)
	if pane.tooShort() {
		_, _ = fmt.Fprintf(w, "    ⚠ 高さが %d 行未満。上限の文が画面の外へ出て検知できないことがある (pane-io.md「読み取り元」)\n", detect.MinDetectableRows)
	}
}

// writePremises はずれを ⚠ で出す。
func writePremises(w io.Writer, rep PremisesReport) {
	if rep.Skipped != "" {
		_, _ = fmt.Fprintf(w, "  %s\n", rep.Skipped)
		return
	}
	for _, f := range rep.Findings {
		if f.OK {
			_, _ = fmt.Fprintf(w, "  OK: %s\n", f.Text)
		} else {
			_, _ = fmt.Fprintf(w, "  ⚠ %s\n", f.Text)
		}
	}
}

func writeRateLimitStates(w io.Writer, rep RateLimitsReport) {
	if rep.ConfigError != "" {
		_, _ = fmt.Fprintf(w, "  設定の読み込みに失敗したため、既定の鮮度閾値で判定します: %s\n", rep.ConfigError)
	}
	for _, s := range rep.Sessions {
		_, _ = fmt.Fprintf(w, "  pane=%s session_id=%s\n", s.PaneID, defaultString(s.SessionID, "(なし)"))
		writeSessionRateLimitState(w, s)
	}
	if len(rep.Sessions) == 0 {
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

func writeSessionRateLimitState(w io.Writer, rep SessionRateLimitReport) {
	if rep.Error != "" {
		_, _ = fmt.Fprintf(w, "  state ファイルの読み込みに失敗: %s\n", rep.Error)
		return
	}
	st := rep.State
	if st == nil {
		_, _ = fmt.Fprintln(w, "  state ファイルはまだありません (statusline 経由の ingest-statusline がまだ rate_limits を受け取っていません)")
		return
	}
	maxAge := time.Duration(st.MaxAgeSeconds) * time.Second
	_, _ = fmt.Fprintf(w, "  observed_at: %s (%s 前)\n", st.ObservedAt.Format(time.RFC3339), time.Duration(st.AgeSeconds)*time.Second)
	if st.Fresh {
		_, _ = fmt.Fprintf(w, "  鮮度: OK (閾値 %s 以内)\n", maxAge)
	} else {
		_, _ = fmt.Fprintf(w, "  鮮度: 古い (閾値 %s を超過。daemon はゲートを開いたまま画面テキストだけで判定します)\n", maxAge)
	}
	_, _ = fmt.Fprintf(w, "  five_hour.used_percentage: %.1f%% (閾値 %.1f%%)\n", st.FiveHourUsed, st.Threshold)
	if st.Exhausted {
		_, _ = fmt.Fprintln(w, "  → 閾値以上: limit 検知時にゲートで抑制しません")
	} else {
		_, _ = fmt.Fprintln(w, "  → 閾値未満: fresh な間は limit と判定されてもゲートで抑制されます")
	}
	_, _ = fmt.Fprintf(w, "  five_hour.resets_at: %s\n", st.FiveHourResetsAt.Format(time.RFC3339))
	if st.SessionIDMismatch != "" {
		_, _ = fmt.Fprintf(w, "  ⚠ ファイル名と中身の session_id が食い違っています (中身: %s)\n", st.SessionIDMismatch)
	}
}

func writeConfigState(w io.Writer, rep ConfigReport) {
	if rep.ReadError != "" {
		_, _ = fmt.Fprintf(w, "  %s の読み込みに失敗: %s\n", rep.Path, rep.ReadError)
		return
	}
	if !rep.Exists {
		_, _ = fmt.Fprintf(w, "  %s は存在しません。全項目デフォルト値で動作します\n", rep.Path)
		return
	}
	_, _ = fmt.Fprintf(w, "  %s は存在します\n", rep.Path)
	if !rep.ValidJSON {
		_, _ = fmt.Fprintln(w, "  JSON として不正です。config.Load() はこれを無視し、全項目デフォルト値にフォールバックします")
	}
	if rep.LoadError != "" {
		_, _ = fmt.Fprintf(w, "  読み込みに失敗しました。デフォルト値で継続します: %s\n", rep.LoadError)
	}
	_, _ = fmt.Fprintln(w, "  実効値 ((*)=デフォルトと異なる値。ファイルの値が反映されている可能性が高い):")
	for _, f := range rep.Fields {
		mark := ""
		if f.Differs {
			mark = " (*)"
		}
		_, _ = fmt.Fprintf(w, "    %-28s = %v%s\n", f.Name, f.Value, mark)
	}
}

func writeDomain(w io.Writer, rep DomainReport) {
	if rep.ConfigError != "" {
		_, _ = fmt.Fprintf(w, "  設定の読み込みに失敗したため、既定 (すべて業務) で判定します: %s\n", rep.ConfigError)
	}
	_, _ = fmt.Fprintf(w, "  homePaths に当たらない cwd: %s\n", rep.Fallback)
	if len(rep.HomePaths) > 0 {
		_, _ = fmt.Fprintf(w, "  私用と判定するパス: %s\n", strings.Join(rep.HomePaths, ", "))
	} else if rep.Fallback == domain.Work {
		_, _ = fmt.Fprintln(w, "  私用と判定するパスはありません (私用の端末なら defaultDomain を home にする)")
	}
	if rep.Dropped > 0 {
		_, _ = fmt.Fprintf(w, "  ⚠ homePaths のうち %d 件は絶対パスでも ~/ 始まりでもないため無視しています\n", rep.Dropped)
	}
	for _, pane := range rep.Panes {
		_, _ = fmt.Fprintf(w, "  pane=%s %s cwd=%s\n", pane.PaneID, pane.Domain, pane.CWD)
	}
}

func writeDaemonState(w io.Writer, rep DaemonReport) {
	if rep.PIDError != "" {
		_, _ = fmt.Fprintf(w, "  PID ファイルの読み込みに失敗: %s\n", rep.PIDError)
		return
	}
	if !rep.Running {
		_, _ = fmt.Fprintln(w, "  daemon は起動していません")
		return
	}
	_, _ = fmt.Fprintf(w, "  daemon 起動中 (pid %d)\n", rep.PID)
	if rep.SnapshotError != "" {
		_, _ = fmt.Fprintf(w, "  status snapshot の読み込みに失敗: %s\n", rep.SnapshotError)
		return
	}
	if rep.Snapshot == nil {
		_, _ = fmt.Fprintln(w, "  status snapshot がまだありません (起動直後の可能性があります)")
		return
	}
	_, _ = fmt.Fprintf(w, "  最終 tick: %s (%s 前)\n", rep.Snapshot.UpdatedAt.Format(time.RFC3339), time.Duration(rep.Snapshot.AgeSeconds)*time.Second)
	_, _ = fmt.Fprintf(w, "  監視中 pane: %d 件\n", rep.Snapshot.MonitoredPanes)
}

func writePeerMessaging(w io.Writer, rep PeerMessagingReport) {
	if rep.Socket == "" {
		_, _ = fmt.Fprintln(w, "  CLAUDE_CODE_MESSAGING_SOCKET: (なし)")
		_, _ = fmt.Fprintln(w, "  Claude Code のセッションの外から呼んだか、そのセッションで cross-session messaging が無効。"+
			"セッション内 (Bash ツール) から呼んで (なし) なら setup skill の references/peer-messaging.md で切り分ける")
		return
	}
	_, _ = fmt.Fprintf(w, "  CLAUDE_CODE_MESSAGING_SOCKET: %s\n", rep.Socket)
	if rep.Exists {
		_, _ = fmt.Fprintln(w, "  OK: socket があります")
	} else {
		_, _ = fmt.Fprintf(w, "  NG: socket がありません (bind に失敗したか、セッションが終了している): %s\n", rep.Error)
	}
	if rep.NestedTmpDir != "" {
		_, _ = fmt.Fprintf(w, "  ⚠ socket が CLAUDE_CODE_TMPDIR (%s) の下にあります。入れ子で起動したセッション (sandbox 内の claude) では bind できず、messaging は使えません\n", rep.NestedTmpDir)
	}
}

func writePaths(w io.Writer, rep PathsReport) {
	_, _ = fmt.Fprintf(w, "  config:      %s\n", rep.Config)
	_, _ = fmt.Fprintf(w, "  state dir:   %s\n", rep.StateDir)
	_, _ = fmt.Fprintf(w, "  rate limits: %s\n", rep.RateLimits)
	_, _ = fmt.Fprintf(w, "  pid file:    %s\n", rep.PIDFile)
	_, _ = fmt.Fprintf(w, "  status file: %s\n", rep.StatusFile)
	_, _ = fmt.Fprintf(w, "  log file:    %s\n", rep.LogFile)
}
