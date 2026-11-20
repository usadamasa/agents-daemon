package blockstate

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"

	"github.com/usadamasa/agents-daemon/internal/multiplexer"
	"github.com/usadamasa/agents-daemon/internal/runtime/backend/rex/wire"
)

// State は shell の block 1 つ分の状態。program_status と process の応答と同じ形で持ち、ToPane に渡す。
type State struct {
	Session       string
	ProgramStatus wire.ProgramStatus
	Process       wire.Process
}

// Ref は Apply が埋めるよう求めた block。flavor は event に載らないので、呼び手が list_blocks で確かめる。
// Block が空なら session_created で、その session の shell の block を全部埋め、session の view も読む。
type Ref struct {
	Session string
	Block   string
	// View なら block は埋めず、session の view (session.view) だけを読み直す。
	View bool
}

// Cache は events の stream を当てて保つ shell の block の状態と、session ごとの view (block の居る window と label)。
// 並行には呼ばない (呼び手が lock を持つ)。
type Cache struct {
	blocks map[string]*State
	views  map[string]wire.SessionView
	// viewFailures は session の view を最後に読もうとして失敗した理由。読めたら消す。
	viewFailures map[string]string
}

// NewCache は全列挙の snapshot (block ID から状態) から Cache を作る。view は PutView で置く。
func NewCache(blocks map[string]*State) *Cache {
	if blocks == nil {
		blocks = map[string]*State{}
	}
	return &Cache{blocks: blocks, views: map[string]wire.SessionView{}, viewFailures: map[string]string{}}
}

// PutView は session の view を置く。持っている view より古い revision の view は捨てる (読み直しの応答が入れ違いに
// 届いたとき)。読めたので、前の読み取りの失敗は消す。
func (c *Cache) PutView(session string, view wire.SessionView) {
	delete(c.viewFailures, session)
	if cur, ok := c.views[session]; ok && view.Revision < cur.Revision {
		return
	}
	c.views[session] = view
}

// PutViewFailure は session の view を読めなかった理由を置く。持っている view は残す (Pane の Location は古いまま
// 出し、LocationReason で古いことを伝える)。
func (c *Cache) PutViewFailure(session string, err error) {
	c.viewFailures[session] = err.Error()
}

// viewChanged は session_view_changed が、持っている view より新しいかを返す。event は revision の順に届くとは限らない。
// rex-premise: preview.30 api session_view_changed は payload に revision だけを持ち、window と session の改名、block の出入りの後に来る
func (c *Cache) viewChanged(session string, raw json.RawMessage, line []byte) (bool, error) {
	var p struct {
		Revision int64 `json:"revision"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return false, fmt.Errorf("session_view_changed の parse に失敗: %w (line=%q)", err, line)
	}
	cur, ok := c.views[session]
	return !ok || p.Revision > cur.Revision, nil
}

// location は block の居る session と window。view を読めていない session の block はゼロ値、window に置かれていない
// block は session だけ。
func (c *Cache) location(blockID string, st *State) multiplexer.Location {
	view, ok := c.views[st.Session]
	if !ok {
		return multiplexer.Location{}
	}
	loc := multiplexer.Location{SessionID: st.Session, SessionLabel: view.Label}
	for _, w := range view.Windows {
		for _, layer := range w.Layers {
			for _, b := range layer.Blocks {
				if b.BlockID == blockID {
					loc.WindowID, loc.WindowLabel = w.WindowID, w.Label
					return loc
				}
			}
		}
	}
	return loc
}

// rex-premise: preview.30 api session.create の最初の block には block_created が来ず、session_created の後にその block の event が流れる

// Apply は stream の 1 行を当てる。block_created と session_created のときは埋めるべき block を、持っている view より
// 新しい session_view_changed のときは読み直す session (View) を返す。
// 読まない type の行と、cache に無い block (shell 以外か、まだ埋めていない) の event は読み捨てる。
// 壊れた行は error にする (呼び手は stream を張り直して全列挙からやり直す)。
func (c *Cache) Apply(line []byte) (Ref, error) {
	var l wire.Event
	if err := json.Unmarshal(line, &l); err != nil {
		return Ref{}, fmt.Errorf("events の行の parse に失敗: %w (line=%q)", err, line)
	}
	switch l.Type {
	case "session_created":
		return Ref{Session: l.Payload.Session.SessionID}, nil
	case "session_destroyed":
		c.dropSession(l.Payload.SessionID)
		delete(c.views, l.Payload.SessionID)
		delete(c.viewFailures, l.Payload.SessionID)
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
	case "session_view_changed":
		changed, err := c.viewChanged(l.Payload.SessionID, ev.Payload, line)
		if err != nil || !changed {
			return Ref{}, err
		}
		return Ref{Session: l.Payload.SessionID, View: true}, nil
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
	var ev wire.BlockEvent
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
		var p wire.ProgramStatusChanged
		if err = json.Unmarshal(ev.Payload, &p); err == nil {
			// record の id は root では欠けるので、payload の id (root は空) で埋める。
			p.Record.ID = p.ID
			st.ProgramStatus.Records = replaceRecord(st.ProgramStatus.Records, p.Record)
		}
	case "program_status_removed":
		var p wire.ProgramStatusRemoved
		if err = json.Unmarshal(ev.Payload, &p); err == nil {
			st.ProgramStatus.Records = slices.DeleteFunc(st.ProgramStatus.Records, func(r wire.Record) bool { return slices.Contains(p.IDs, r.ID) })
		}
	case "process_changed":
		// 前景の置き換え。stream は順序を保つので generation は見ない。
		err = json.Unmarshal(ev.Payload, &st.Process)
	case "child_exited":
		// --keep-open の block は終了後に shell が前景へ戻らず process_changed が来ない。前景を空にして、
		// 残った record を polling の経路 (終了後の応答に foreground が無い) と同じく未検証にする。
		// rex-premise: preview.30 api child_exited の後も idle の record は残る。process_changed は来ないか、来ても foreground は null
		st.Process.Foreground = nil
	}
	if err != nil {
		return fmt.Errorf("%s の parse に失敗: %w (line=%q)", ev.Name, err, line)
	}
	return nil
}

// replaceRecord は id が同じ record を置き換え、無ければ足す。report は record を丸ごと置き換える (OSC 7501)。
func replaceRecord(records []wire.Record, r wire.Record) []wire.Record {
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
		pane := ToPane(id, st.ProgramStatus, st.Process, sessions[id])
		pane.Location = c.location(id, st)
		pane.LocationReason = c.viewFailures[st.Session]
		panes = append(panes, pane)
	}
	return panes
}
