// Package blockstate は rex の shell の block の状態を持つ。`block call program_status` / `process` と `block ls` の
// 応答の形、`events --all --json` の行の形、行を当てて保つ cache、その状態から multiplexer.Pane への写像。
// rex を呼ばない (I/O は親の rexcli が持つ)。
package blockstate

import (
	"fmt"
	"path/filepath"

	"github.com/usadamasa/agents-daemon/internal/multiplexer"
)

// ShellFlavor は terminal の block の flavor。これ以外の block は program_status を持たない。
// rex-premise: preview.21 cli block ls の flavor の値
const ShellFlavor = "com.superlogical.terminal.shell"

// ScreenAlternate は `block call modes` の screen が alternate screen を指す値。これ以外は primary と見る
// (output_schema の指示)。
const ScreenAlternate = "alternate"

// ProgramStatus は `block call program_status` の応答 (OSC 7501 の record)。
type ProgramStatus struct {
	Records []Record `json:"records"`
}

// Record の ID は root record では無いか空で、subagent では agent ID になる。
// Owner は rex が足す、record を書いた process (OSC 7501 の仕様には無い)。
type Record struct {
	ID    string `json:"id"`
	State string `json:"state"`
	App   string `json:"app"`
	// Kind は blocked の種類、Msg は状況の説明。どちらも任意 (無い状態がある)。
	Kind  string `json:"kind"`
	Msg   string `json:"msg"`
	Owner struct {
		PID int `json:"pid"`
	} `json:"owner"`
}

// Process は `block call process` の応答と process_changed の payload のうち使う部分。
type Process struct {
	Foreground *Foreground `json:"foreground"`
}

// Foreground は block の前景の process。
type Foreground struct {
	PID   int    `json:"pid"`
	Argv0 string `json:"argv0"`
	CWD   string `json:"cwd"`
}

// BlockList は `block ls -s S --json` の応答のうち使う部分。
type BlockList struct {
	Blocks []struct {
		BlockID string `json:"block_id"`
		Flavor  string `json:"flavor"`
	} `json:"blocks"`
}

// RecordFields と ForegroundFields は Record と Foreground が読む欄の dotted path。doctor の点検が応答に
// これらの key があるかを見る (json.Unmarshal のゼロ値では owner.pid の 0 と欄無しを見分けられない)。
// BlockEvents は Cache が読む block の event で、doctor が `block inspect` の api.events にあるかを見る
// (block_created / block_closed は session の event で、block の inspect には載らない)。
var (
	RecordFields     = []string{"state", "app", "owner.pid"}
	ForegroundFields = []string{"pid", "argv0", "cwd"}
	BlockEvents      = []string{"program_status_changed", "program_status_removed", "process_changed"}
)

// claudeApp は Claude Code が OSC 7501 の app に書く値。2.1.302 のバイナリでは固定で、
// rex preview.13 までが process 名から補っていた `claude` は preview.15 では来ない (#69)。
// cc-premise: 2.1.310 osc7501 app=claude-code 固定
// rex-premise: preview.21 program_status rex は record を補わず、Claude Code の自己申告だけを返す
const claudeApp = "claude-code"

// ToPane は rex の応答を中立の Pane へ写す。TerminalTitle は consumer が読まないので埋めない
// (title method の分だけ fork が増える)。
func ToPane(blockID string, ps ProgramStatus, proc Process, sessionID string) multiplexer.Pane {
	pane := multiplexer.Pane{
		PaneID:      blockID,
		AgentStatus: multiplexer.AgentStatusUnknown,
		SessionID:   sessionID,
	}
	if proc.Foreground != nil {
		pane.CWD = proc.Foreground.CWD
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
		// --shell=none で起動した block では argv0 が絶対パスになる (実測)。
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
func isForeground(record *Record, proc Process) bool {
	return proc.Foreground != nil && record.Owner.PID == proc.Foreground.PID
}

// unverifiedReason は Claude Code の record を使えなかった理由。owner が無いのは rex の版が変わって
// 欄が消えた (か名前が変わった) ときで、放置すると全 pane が unknown のまま判定が黙って止まる。
func unverifiedReason(record *Record, foregroundPID int) string {
	if record.Owner.PID == 0 {
		return "record に owner.pid が無い (rex の版が変わった可能性。record を使えず agent_status が unknown のままになる)"
	}
	return fmt.Sprintf("record の owner (pid %d) が foreground の process (pid %d) と違う (前の Claude の record が残っている)", record.Owner.PID, foregroundPID)
}

// rootRecord は subagent ではない Claude Code 本体の record を返す。無ければ nil。
func rootRecord(records []Record) *Record {
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
