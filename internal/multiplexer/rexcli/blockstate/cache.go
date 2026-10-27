package blockstate

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"

	"github.com/usadamasa/agents-daemon/internal/multiplexer"
)

// State は shell の block 1 つ分の状態。program_status と process の応答と同じ形で持ち、ToPane に渡す。
type State struct {
	Session       string
	ProgramStatus ProgramStatus
	Process       Process
}

// Ref は Apply が埋めるよう求めた block。flavor は event に載らないので、呼び手が block ls で確かめる。
// Block が空なら session_created で、その session の shell の block を全部埋める。
type Ref struct {
	Session string
	Block   string
}

// Cache は events の stream を当てて保つ shell の block の状態。並行には呼ばない (呼び手が lock を持つ)。
type Cache struct {
	blocks map[string]*State
}

// NewCache は全列挙の snapshot (block ID から状態) から Cache を作る。
func NewCache(blocks map[string]*State) *Cache {
	if blocks == nil {
		blocks = map[string]*State{}
	}
	return &Cache{blocks: blocks}
}

// streamLine は `events --all --json` の 1 行のうち読む部分。
// rex-premise: preview.21 cli events --all --json は session_event の payload.event に block_event / block_created / block_closed を、top level に session_created / session_destroyed を流す
// rex-premise: preview.21 cli session new の最初の block には block_created が来ず、session_created の後にその block の event が流れる
type streamLine struct {
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

// blockEvent は block_event の payload。Payload の形は Name で決まる。
type blockEvent struct {
	BlockID string          `json:"block_id"`
	Name    string          `json:"name"`
	Payload json.RawMessage `json:"payload"`
}

// rex-premise: preview.21 cli program_status_changed は {id, record, reason}、program_status_removed は {ids, reason}、process_changed は {generation, foreground}
type (
	programStatusChanged struct {
		ID     string `json:"id"`
		Record Record `json:"record"`
	}
	programStatusRemoved struct {
		IDs []string `json:"ids"`
	}
)

// Relevant は Apply が読む type の行か。client の出入りのような雑音は rex の CLI の呼び出しのたびに流れるので、
// 呼び手はこれで落としてから溜める。壊れた行は通し、Apply に error を出させる。
func Relevant(line []byte) bool {
	var l streamLine
	if err := json.Unmarshal(line, &l); err != nil {
		return true
	}
	switch l.Type {
	case "session_created", "session_destroyed":
		return true
	case "session_event":
		if l.Payload.Event == nil {
			return false
		}
		switch l.Payload.Event.Type {
		case "block_event", "block_created", "block_closed":
			return true
		}
	}
	return false
}

// Apply は stream の 1 行を当てる。block_created と session_created のときだけ、埋めるべき block を返す。
// 読まない type の行と、cache に無い block (shell 以外か、まだ埋めていない) の event は読み捨てる。
// 壊れた行は error にする (呼び手は stream を張り直して全列挙からやり直す)。
func (c *Cache) Apply(line []byte) (Ref, error) {
	var l streamLine
	if err := json.Unmarshal(line, &l); err != nil {
		return Ref{}, fmt.Errorf("events の行の parse に失敗: %w (line=%q)", err, line)
	}
	switch l.Type {
	case "session_created":
		return Ref{Session: l.Payload.Session.SessionID}, nil
	case "session_destroyed":
		c.dropSession(l.Payload.SessionID)
		return Ref{}, nil
	case "session_event":
	default:
		return Ref{}, nil
	}
	if l.Payload.Event == nil {
		return Ref{}, nil
	}
	ev := l.Payload.Event
	switch ev.Type {
	case "block_event":
		return Ref{}, c.applyBlockEvent(ev.Payload, line)
	case "block_created", "block_closed":
		var p struct {
			BlockID string `json:"block_id"`
		}
		if err := json.Unmarshal(ev.Payload, &p); err != nil {
			return Ref{}, fmt.Errorf("%s の parse に失敗: %w (line=%q)", ev.Type, err, line)
		}
		if ev.Type == "block_closed" {
			delete(c.blocks, p.BlockID)
			return Ref{}, nil
		}
		return Ref{Session: l.Payload.SessionID, Block: p.BlockID}, nil
	}
	return Ref{}, nil
}

func (c *Cache) applyBlockEvent(raw json.RawMessage, line []byte) error {
	var ev blockEvent
	if err := json.Unmarshal(raw, &ev); err != nil {
		return fmt.Errorf("block_event の parse に失敗: %w (line=%q)", err, line)
	}
	st, ok := c.blocks[ev.BlockID]
	if !ok {
		return nil
	}
	var err error
	switch ev.Name {
	case "program_status_changed":
		var p programStatusChanged
		if err = json.Unmarshal(ev.Payload, &p); err == nil {
			// record の id は root では欠けるので、payload の id (root は空) で埋める。
			p.Record.ID = p.ID
			st.ProgramStatus.Records = replaceRecord(st.ProgramStatus.Records, p.Record)
		}
	case "program_status_removed":
		var p programStatusRemoved
		if err = json.Unmarshal(ev.Payload, &p); err == nil {
			st.ProgramStatus.Records = slices.DeleteFunc(st.ProgramStatus.Records, func(r Record) bool { return slices.Contains(p.IDs, r.ID) })
		}
	case "process_changed":
		// 前景の置き換え。stream は順序を保つので generation は見ない。
		err = json.Unmarshal(ev.Payload, &st.Process)
	}
	if err != nil {
		return fmt.Errorf("%s の parse に失敗: %w (line=%q)", ev.Name, err, line)
	}
	return nil
}

// replaceRecord は id が同じ record を置き換え、無ければ足す。report は record を丸ごと置き換える (OSC 7501)。
func replaceRecord(records []Record, r Record) []Record {
	for i := range records {
		if records[i].ID == r.ID {
			records[i] = r
			return records
		}
	}
	return append(records, r)
}

func (c *Cache) dropSession(session string) {
	for id, st := range c.blocks {
		if st.Session == session {
			delete(c.blocks, id)
		}
	}
}

// Put は block_created の後に埋めた block を足す。
func (c *Cache) Put(ref Ref, st *State) {
	c.blocks[ref.Block] = st
}

// Panes は cache を block ID の順に Pane へ写す。sessions は block ID から session_id への対応。
func (c *Cache) Panes(sessions map[string]string) []multiplexer.Pane {
	ids := make([]string, 0, len(c.blocks))
	for id := range c.blocks {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	panes := make([]multiplexer.Pane, 0, len(ids))
	for _, id := range ids {
		st := c.blocks[id]
		panes = append(panes, ToPane(id, st.ProgramStatus, st.Process, sessions[id]))
	}
	return panes
}
