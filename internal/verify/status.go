package verify

import (
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/usadamasa/agents-daemon/internal/applog"
	"github.com/usadamasa/agents-daemon/internal/apppath"
	"github.com/usadamasa/agents-daemon/internal/daemon"
)

// statusLogTailLines は `status` が末尾に表示する現行ログの行数。
const statusLogTailLines = 20

// StatusReport は status の結果。daemon が起動していなければ Running だけが埋まる。
type StatusReport struct {
	Running bool `json:"running"`
	PID     int  `json:"pid,omitempty"`
	// Snapshot は status.json の内容。今の daemon が書いたものでなければ nil (前の daemon の残り物は見せない)。
	Snapshot *daemon.StatusSnapshot `json:"snapshot"`
	// LogTail は現行ログの末尾。読めなければ LogError に理由が入る。
	LogTail  []string `json:"log_tail"`
	LogError string   `json:"log_error,omitempty"`
}

// RunStatus は daemon の稼働状況、status.json のスナップショット、現行ログの
// 末尾を w へ書く。daemon が起動していない場合はそれを明言して即座に戻る。
func RunStatus(w io.Writer, p apppath.Paths, now time.Time) error {
	rep, err := CollectStatus(p)
	if err != nil {
		return err
	}
	writeStatus(w, rep, now)
	return nil
}

// CollectStatus は status の結果を集める。PID ファイルと status.json を読めないときだけ error。
func CollectStatus(p apppath.Paths) (StatusReport, error) {
	rep := StatusReport{LogTail: []string{}}
	pid, err := daemon.ReadPIDFile(p.PIDFile())
	if err != nil {
		return rep, err
	}
	if pid == 0 || !daemon.PIDAlive(pid) {
		return rep, nil
	}
	rep.Running, rep.PID = true, pid

	snap, err := daemon.ReadStatusSnapshot(p.StatusFile())
	if err != nil {
		return rep, err
	}
	// snap.PID を現に動いている pid と突き合わせるのは、前のデーモンが残した
	// スナップショットを今のデーモンのものとして見せないため。突き合わせないと
	// 「起動: 16 分前」のように、実際には数秒前に起きたデーモンの情報として
	// 古い時刻が表示される (実際に踏んだ)。
	if snap != nil && snap.PID == pid {
		rep.Snapshot = snap
	}

	tail, err := applog.TailLogLines(p.LogFile(), statusLogTailLines)
	if err != nil {
		rep.LogError = err.Error()
		return rep, nil
	}
	rep.LogTail = append(rep.LogTail, tail...)
	return rep, nil
}

// writeStatus は StatusReport を人が読む形で w へ書く。
func writeStatus(w io.Writer, rep StatusReport, now time.Time) {
	if !rep.Running {
		_, _ = fmt.Fprintln(w, "daemon は起動していません")
		return
	}
	_, _ = fmt.Fprintf(w, "daemon 起動中 (pid %d)\n", rep.PID)
	printSnapshot(w, rep.Snapshot, now)
	if rep.LogError != "" {
		_, _ = fmt.Fprintf(w, "ログを読めません: %s\n", rep.LogError)
		return
	}
	if len(rep.LogTail) > 0 {
		_, _ = fmt.Fprintln(w, "--- ログ (末尾) ---")
		for _, line := range rep.LogTail {
			_, _ = fmt.Fprintln(w, line)
		}
	}
}

// printSnapshot は status.json の内容を書く。nil は今の daemon の snapshot がまだ無い状態。
func printSnapshot(w io.Writer, snap *daemon.StatusSnapshot, now time.Time) {
	if snap == nil {
		_, _ = fmt.Fprintln(w, "status snapshot がまだありません (起動直後の可能性があります)")
		return
	}
	_, _ = fmt.Fprintf(w, "backend: %s\n", backendColumn(snap.Backend))
	_, _ = fmt.Fprintf(w, "起動: %s (%s 前)\n", snap.StartedAt.Format(time.RFC3339), now.Sub(snap.StartedAt).Round(time.Second))
	_, _ = fmt.Fprintf(w, "最終更新: %s (%s 前)\n", snap.UpdatedAt.Format(time.RFC3339), now.Sub(snap.UpdatedAt).Round(time.Second))
	if len(snap.Panes) == 0 {
		_, _ = fmt.Fprintln(w, "監視中の pane はありません")
		return
	}
	_, _ = fmt.Fprintln(w, "監視中の pane:")
	for _, pn := range snap.Panes {
		wait := "-"
		if pn.WaitUntil != nil {
			wait = pn.WaitUntil.Format(time.RFC3339)
		}
		_, _ = fmt.Fprintf(w, "  %s  %s  agent=%s%s monitor=%s attempts=%d wait_until=%s last=%s\n",
			pn.PaneID, pn.CWD, pn.AgentStatus, recordColumns(pn), pn.MonitorStatus, pn.Attempts, wait, pn.LastOutcome)
	}
}

// recordColumns は Claude Code の自己申告の kind と msg を、あるときだけ agent= の後ろに足す。
func recordColumns(pn daemon.PaneStatus) string {
	s := ""
	if pn.AgentKind != "" {
		s += " kind=" + pn.AgentKind
	}
	if pn.AgentMessage != "" {
		s += " msg=" + strconv.Quote(pn.AgentMessage)
	}
	return s
}

// backendColumn は backend の表示。記録を始める前の daemon が書いた snapshot では空になる。
func backendColumn(kind string) string {
	if kind == "" {
		return "(記録なし)"
	}
	return kind
}
