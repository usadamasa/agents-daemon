// Package wire は rex の応答と event の形のうち、agents-daemon が読む部分の型を持つ。program_status の record、
// process、list_blocks、event の envelope。I/O を持たず、daemon の語彙 (multiplexer.Pane) も知らない。
// 型は control API の応答と CLI の出力のどちらにも同じ形で当たる (CLI は API の応答をそのまま出す)。
package wire

import "encoding/json"

// ShellFlavor は terminal の block の flavor。これ以外の block は program_status を持たない。
// rex-premise: preview.30 api list_blocks の flavor の値
const ShellFlavor = "com.superlogical.terminal.shell"

// ScreenAlternate は modes の screen が alternate screen を指す値。これ以外は primary と見る (output_schema の指示)。
const ScreenAlternate = "alternate"

// ProgramStatus は program_status の応答 (OSC 7501 の record)。
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

// Process は process の応答と process_changed の payload のうち使う部分。
type Process struct {
	Foreground *Foreground `json:"foreground"`
}

// Foreground は block の前景の process。
type Foreground struct {
	PID   int    `json:"pid"`
	Argv0 string `json:"argv0"`
	CWD   string `json:"cwd"`
}

// BlockList は list_blocks の応答のうち使う部分。
type BlockList struct {
	Blocks []struct {
		BlockID string `json:"block_id"`
		Flavor  string `json:"flavor"`
	} `json:"blocks"`
}

// Size は size の応答のうち使う部分。
type Size struct {
	Rows    int `json:"rows"`
	Columns int `json:"columns"`
}

// Modes は modes の応答のうち使う部分。
type Modes struct {
	Screen string `json:"screen"`
}

// RecordFields と ForegroundFields は Record と Foreground が読む欄の dotted path。doctor の点検が応答に
// これらの key があるかを見る (json.Unmarshal のゼロ値では owner.pid の 0 と欄無しを見分けられない)。
// BlockEvents は購読の cache が読む block の event で、doctor が block の api.events にあるかを見る
// (block_created / block_closed は session の event で、block の api には載らない)。
var (
	RecordFields     = []string{"state", "app", "owner.pid"}
	ForegroundFields = []string{"pid", "argv0", "cwd"}
	BlockEvents      = []string{"program_status_changed", "program_status_removed", "process_changed", "child_exited"}
)

// Event は event 1 件 (control API の event message の payload、CLI の events --json の 1 行) のうち読む部分。
// rex-premise: preview.30 api session_event の payload.event に block_event / block_created / block_closed を、top level に session_created / session_destroyed を流す
type Event struct {
	Type    string `json:"type"`
	Payload struct {
		SessionID string `json:"session_id"`
		Session   struct {
			SessionID string `json:"session_id"`
		} `json:"session"`
		Event *struct {
			Type    string          `json:"type"`
			Payload json.RawMessage `json:"payload"`
		} `json:"event"`
	} `json:"payload"`
}

// BlockEvent は block_event の payload。Payload の形は Name で決まる。
type BlockEvent struct {
	BlockID string          `json:"block_id"`
	Name    string          `json:"name"`
	Payload json.RawMessage `json:"payload"`
}

// ProgramStatusChanged と ProgramStatusRemoved は block_event の payload。
// rex-premise: preview.30 api program_status_changed は {id, record, reason}、program_status_removed は {ids, reason}、process_changed は {generation, foreground}
type (
	ProgramStatusChanged struct {
		ID     string `json:"id"`
		Record Record `json:"record"`
	}
	ProgramStatusRemoved struct {
		IDs []string `json:"ids"`
	}
)
