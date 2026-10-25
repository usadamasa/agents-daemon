package verify

import (
	"fmt"
	"time"

	"github.com/usadamasa/agents-daemon/internal/apppath"
	"github.com/usadamasa/agents-daemon/internal/daemon/state"
)

// SessionExited は、動いている daemon が after より後に書いた status.json にセッションの pane が無いことを確かめる。
// ボードの「パーキングへ移す」(parking.Adopt) の前提で、生きているセッションのタスクの記録を消さないためのもの
// (models/handoff の LiveTaskKept)。daemon が動いていない、status.json が無いか前の daemon のもの、after (タスクの
// 記録の時刻) より古い、pane がある、のどれかなら理由を返す。status と同じ読み方 (state.Running) で見る。
func SessionExited(p apppath.Paths, sessionID string, after time.Time) error {
	pid, snap, err := state.Running(p)
	if err != nil {
		return err
	}
	if pid == 0 {
		return fmt.Errorf("daemon が動いていないので、セッション %s が終了したか分からない", sessionID)
	}
	if snap == nil {
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
