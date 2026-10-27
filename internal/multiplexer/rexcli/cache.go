package rexcli

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"

	"github.com/usadamasa/agents-daemon/internal/multiplexer"
)

// blockState は shell の block 1 つ分の cache。program_status と process の応答と同じ形で持ち、toPane に渡す。
type blockState struct {
	session string
	ps      programStatusResult
	proc    processResult
}

// blockRef は block_created が埋めるよう求めた block。flavor は event に載らないので、呼び手が block ls で確かめる。
type blockRef struct {
	session string
	block   string
}

// paneCache は events の stream を当てて保つ shell の block の状態。並行には呼ばない (呼び手が lock を持つ)。
type paneCache struct {
	blocks map[string]*blockState
}

func newPaneCache(blocks map[string]*blockState) *paneCache {
	if blocks == nil {
		blocks = map[string]*blockState{}
	}
	return &paneCache{blocks: blocks}
}

// streamLine は `events --all --json` の 1 行のうち読む部分。
// rex-premise: preview.21 cli events --all --json は session_event の payload.event に block_event / block_created / block_closed を、top level に session_destroyed を流す
type streamLine struct {
	Type    string `json:"type"`
	Payload struct {
		SessionID string `json:"session_id"`
		Event     *struct {
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
		ID     string              `json:"id"`
		Record programStatusRecord `json:"record"`
	}
	programStatusRemoved struct {
		IDs []string `json:"ids"`
	}
)

// apply は stream の 1 行を当てる。block_created のときだけ、埋めるべき block を返す。
// 読まない type の行と、cache に無い block (shell 以外か、まだ埋めていない) の event は読み捨てる。
// 壊れた行は error にする (呼び手は stream を張り直して全列挙からやり直す)。
func (c *paneCache) apply(line []byte) (blockRef, error) {
	var l streamLine
	if err := json.Unmarshal(line, &l); err != nil {
		return blockRef{}, fmt.Errorf("events の行の parse に失敗: %w (line=%q)", err, line)
	}
	switch l.Type {
	case "session_destroyed":
		c.dropSession(l.Payload.SessionID)
		return blockRef{}, nil
	case "session_event":
	default:
		return blockRef{}, nil
	}
	if l.Payload.Event == nil {
		return blockRef{}, nil
	}
	ev := l.Payload.Event
	switch ev.Type {
	case "block_event":
		return blockRef{}, c.applyBlockEvent(ev.Payload, line)
	case "block_created", "block_closed":
		var p struct {
			BlockID string `json:"block_id"`
		}
		if err := json.Unmarshal(ev.Payload, &p); err != nil {
			return blockRef{}, fmt.Errorf("%s の parse に失敗: %w (line=%q)", ev.Type, err, line)
		}
		if ev.Type == "block_closed" {
			delete(c.blocks, p.BlockID)
			return blockRef{}, nil
		}
		return blockRef{session: l.Payload.SessionID, block: p.BlockID}, nil
	}
	return blockRef{}, nil
}

func (c *paneCache) applyBlockEvent(raw json.RawMessage, line []byte) error {
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
			st.ps.Records = replaceRecord(st.ps.Records, p.Record)
		}
	case "program_status_removed":
		var p programStatusRemoved
		if err = json.Unmarshal(ev.Payload, &p); err == nil {
			st.ps.Records = slices.DeleteFunc(st.ps.Records, func(r programStatusRecord) bool { return slices.Contains(p.IDs, r.ID) })
		}
	case "process_changed":
		// 前景の置き換え。stream は順序を保つので generation は見ない。
		err = json.Unmarshal(ev.Payload, &st.proc)
	}
	if err != nil {
		return fmt.Errorf("%s の parse に失敗: %w (line=%q)", ev.Name, err, line)
	}
	return nil
}

// replaceRecord は id が同じ record を置き換え、無ければ足す。report は record を丸ごと置き換える (OSC 7501)。
func replaceRecord(records []programStatusRecord, r programStatusRecord) []programStatusRecord {
	for i := range records {
		if records[i].ID == r.ID {
			records[i] = r
			return records
		}
	}
	return append(records, r)
}

func (c *paneCache) dropSession(session string) {
	for id, st := range c.blocks {
		if st.session == session {
			delete(c.blocks, id)
		}
	}
}

// put は block_created の後に埋めた block を足す。
func (c *paneCache) put(ref blockRef, st *blockState) {
	c.blocks[ref.block] = st
}

// panes は cache を block ID の順に Pane へ写す。sessions は block ID から session_id への対応。
func (c *paneCache) panes(sessions map[string]string) []multiplexer.Pane {
	ids := make([]string, 0, len(c.blocks))
	for id := range c.blocks {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	panes := make([]multiplexer.Pane, 0, len(ids))
	for _, id := range ids {
		st := c.blocks[id]
		panes = append(panes, toPane(id, st.ps, st.proc, sessions[id]))
	}
	return panes
}
