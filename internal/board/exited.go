package board

import (
	"fmt"
	"time"

	"github.com/usadamasa/agents-daemon/internal/apppath"
	"github.com/usadamasa/agents-daemon/internal/daemon"
)

// SessionExited は、今の daemon が after より後に書いた status.json にセッションの pane が無いことを確かめる。
// ボードの「パーキングへ移す」(parking.Adopt) の前提で、生きているセッションのタスクの記録を消さないためのもの
// (models/handoff の LiveTaskKept)。daemon が動いていない、status.json が前の daemon のもの、after (タスクの記録の
// 時刻) より古い、pane がある、のどれかなら理由を返す。ボードが pane を空として描く条件 (livePanes) と同じ判断を、
// 描いた後ではなく書き換える直前にもう一度する。
func SessionExited(p apppath.Paths, sessionID string, after time.Time) error {
	pid, snap, err := readDaemon(p)
	if err != nil {
		return err
	}
	if pid == 0 {
		return fmt.Errorf("daemon が動いていないので、セッション %s が終了したか分からない", sessionID)
	}
	if snap == nil || snap.PID != pid {
		return fmt.Errorf("daemon (pid %d) がまだ status.json を書いていないので、セッション %s が終了したか分からない", pid, sessionID)
	}
	if !snap.UpdatedAt.After(after) {
		return fmt.Errorf("status.json (%s) がタスクの記録より古いので、セッション %s が終了したか分からない", snap.UpdatedAt.Format(time.RFC3339), sessionID)
	}
	for _, pane := range snap.Panes {
		if pane.SessionID == sessionID {
			return fmt.Errorf("セッション %s は pane %s で生きている", sessionID, pane.PaneID)
		}
	}
	return nil
}

// readDaemon は動いている daemon の pid (動いていなければ 0) と、その daemon が書いた status.json を読む。
// 前の daemon が残した status.json は snap.PID で見分ける (status コマンドと同じ判断)。
func readDaemon(p apppath.Paths) (pid int, snap *daemon.StatusSnapshot, err error) {
	if pid, err = daemon.ReadPIDFile(p.PIDFile()); err != nil {
		return 0, nil, err
	}
	if !daemon.PIDAlive(pid) {
		return 0, nil, nil
	}
	if snap, err = daemon.ReadStatusSnapshot(p.StatusFile()); err != nil {
		return 0, nil, err
	}
	return pid, snap, nil
}
