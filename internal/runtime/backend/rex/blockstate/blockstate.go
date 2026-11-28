// Package blockstate は rex の shell の block の状態を daemon の語彙へ写す。block の状態 (program_status と process) から
// multiplexer.Pane への写像と、event を当てて保つ cache を持つ。応答と event の形は wire、rex を呼ぶのは親の rex。
package blockstate

import (
	"fmt"
	"path/filepath"

	"github.com/usadamasa/agents-daemon/internal/multiplexer"
	"github.com/usadamasa/agents-daemon/internal/runtime/backend/rex/wire"
)

// claudeApp は Claude Code が OSC 7501 の app に書く値。2.1.302 のバイナリでは固定で、
// rex preview.13 までが process 名から補っていた `claude` は preview.15 では来ない (#69)。
// cc-premise: 2.1.310 osc7501 app=claude-code 固定
// rex-premise: preview.37 program_status rex は record を補わず、Claude Code の自己申告だけを返す
const claudeApp = "claude-code"

// ToPane は rex の応答を中立の Pane へ写す。TerminalTitle は consumer が読まないので埋めない
// (title method の分だけ呼び出しが増える)。
func ToPane(blockID string, ps wire.ProgramStatus, proc wire.Process, sessionID string) multiplexer.Pane {
	pane := multiplexer.Pane{
		PaneID:      blockID,
		AgentStatus: multiplexer.AgentStatusUnknown,
		SessionID:   sessionID,
	}
	if proc.Foreground != nil {
		pane.CWD = proc.Foreground.CWD
		pane.PID = proc.Foreground.PID
	}
	root := rootRecord(ps.Records)
	switch {
	case root != nil && root.App == claudeApp && isForeground(root, proc):
		pane.Agent = "claude"
		pane.AgentStatus = agentStatus(root.State)
		pane.AgentKind = root.Kind
		pane.AgentMessage = root.Msg
	case proc.Foreground != nil && filepath.Base(proc.Foreground.Argv0) == "claude":
		// record が無い (起動直後の自己申告がまだ) か、record はあるが使えない。
		// shell を挟まずに起動した block では argv0 が絶対パスになる (実測)。
		pane.Agent = "claude"
		if root != nil && root.App == claudeApp {
			pane.UnknownReason = unverifiedReason(root, proc.Foreground.PID)
		}
	}
	return pane
}

// isForeground は record を書いた process が今も foreground にいるかを返す。
// OSC 7501 の仕様では done と error の record は process の終了と shell prompt を生き延びるので、
// Claude Code が SIGKILL で終わる (/exit なら state=clear を出して消す) と素の shell に
// app=claude-code の done が残る (実測、#69)。その block を Claude の pane と見て
// 再開の文面を shell へ打たないよう、foreground の pid が一致する record だけを使う。
func isForeground(record *wire.Record, proc wire.Process) bool {
	return proc.Foreground != nil && record.Owner.PID == proc.Foreground.PID
}

// unverifiedReason は Claude Code の record を使えなかった理由。owner が無いのは rex の版が変わって
// 欄が消えた (か名前が変わった) ときで、放置すると全 pane が unknown のまま判定が黙って止まる。
func unverifiedReason(record *wire.Record, foregroundPID int) string {
	if record.Owner.PID == 0 {
		return "record に owner.pid が無い (rex の版が変わった可能性。record を使えず agent_status が unknown のままになる)"
	}
	return fmt.Sprintf("record の owner (pid %d) が foreground の process (pid %d) と違う (前の Claude の record が残っている)", record.Owner.PID, foregroundPID)
}

// rootRecord は subagent ではない Claude Code 本体の record を返す。無ければ nil。
func rootRecord(records []wire.Record) *wire.Record {
	for i := range records {
		if records[i].ID == "" {
			return &records[i]
		}
	}
	return nil
}

func agentStatus(state string) multiplexer.AgentStatus {
	switch s := multiplexer.AgentStatus(state); s {
	case multiplexer.AgentStatusWorking, multiplexer.AgentStatusDone,
		multiplexer.AgentStatusIdle, multiplexer.AgentStatusBlocked, multiplexer.AgentStatusError:
		return s
	default:
		return multiplexer.AgentStatusUnknown
	}
}
