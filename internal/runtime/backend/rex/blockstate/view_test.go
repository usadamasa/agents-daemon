package blockstate

import (
	"encoding/json"
	"strconv"
	"testing"

	"github.com/usadamasa/agents-daemon/internal/multiplexer"
	"github.com/usadamasa/agents-daemon/internal/runtime/backend/rex/wire"
)

const (
	windowA     = "window:01a1f8fb-31ab-7af4-a1c6-699a246f65ab"
	windowB     = "window:01a1f8fb-e4b1-72f1-b036-72346af26665"
	blockOther  = "block:01a1f8fb-e4a3-7cc1-869a-73d1a89c2e92"
	blockLoose  = "block:01a1f8fc-86c5-773f-a013-8a5e89b6cb4f"
	sessionNone = "session:01a1f8fc-0000-7000-8000-000000000000"
)

// viewLine は session_view_changed の行を 2026-11-20 の実測の形で組む。
func viewLine(session string, revision int) string {
	return `{"type":"session_event","payload":{"session_id":"` + session + `","event":{"type":"session_view_changed","payload":{"revision":` +
		strconv.Itoa(revision) + `}}}}`
}

// sessionView は session.view の応答を 2026-11-20 の実測の形 (window ごとの layer の blocks) で組む。
func sessionView(t *testing.T, session, label string, revision int, windows ...[3]string) wire.SessionView {
	t.Helper()
	type block struct {
		BlockID string `json:"block_id"`
	}
	type layer struct {
		Blocks []block `json:"blocks"`
	}
	type window struct {
		WindowID string  `json:"window_id"`
		Label    string  `json:"label"`
		Layers   []layer `json:"layers"`
	}
	raw := struct {
		Revision  int      `json:"revision"`
		SessionID string   `json:"session_id"`
		Label     string   `json:"label"`
		Windows   []window `json:"windows"`
	}{Revision: revision, SessionID: session, Label: label}
	for _, w := range windows {
		raw.Windows = append(raw.Windows, window{WindowID: w[0], Label: w[1], Layers: []layer{{Blocks: []block{{BlockID: w[2]}}}}})
	}
	data, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	var v wire.SessionView
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatalf("session.view の parse に失敗: %v", err)
	}
	return v
}

// Panes は block の居る session と window の ID と label を view から埋める。window に置かれていない block は
// session だけ、view を読めていない session の block は空のまま。
func TestCache_PanesLocation(t *testing.T) {
	c := NewCache(map[string]*State{
		blockOwn:   state(t, sessionOwn, psEmpty, processJSON(pidOwn, "claude", cwdOwn)),
		blockOther: state(t, sessionOwn, psEmpty, processJSON(pidOwn, "claude", cwdOwn)),
		blockLoose: state(t, sessionOwn, psEmpty, processJSON(pidOwn, "claude", cwdOwn)),
		blockProbe: state(t, sessionNone, psEmpty, processJSON(pidOwn, "claude", cwdOwn)),
	})
	c.PutView(sessionOwn, sessionView(t, sessionOwn, "repo-scan-sync", 3,
		[3]string{windowA, "peer-a", blockOwn},
		[3]string{windowB, "peer-b", blockOther},
	))

	got := map[string]multiplexer.Location{}
	for _, p := range c.Panes(nil) {
		got[p.PaneID] = p.Location
	}
	want := map[string]multiplexer.Location{
		blockOwn:   {SessionID: sessionOwn, SessionLabel: "repo-scan-sync", WindowID: windowA, WindowLabel: "peer-a"},
		blockOther: {SessionID: sessionOwn, SessionLabel: "repo-scan-sync", WindowID: windowB, WindowLabel: "peer-b"},
		blockLoose: {SessionID: sessionOwn, SessionLabel: "repo-scan-sync"},
		blockProbe: {},
	}
	for id, w := range want {
		if got[id] != w {
			t.Errorf("%s の Location = %+v, want %+v", id, got[id], w)
		}
	}
}

// session_view_changed は持っている view より新しい revision のときだけ読み直しを求める。event は revision の順に
// 届くとは限らない (rex の docs の session.view)。
func TestCache_ViewChanged(t *testing.T) {
	c := NewCache(nil)
	ref, err := c.Apply([]byte(viewLine(sessionOwn, 5)))
	if err != nil || ref != (Ref{Session: sessionOwn, View: true}) {
		t.Fatalf("view の無い session の Apply = %+v, %v, want 読み直し", ref, err)
	}
	c.PutView(sessionOwn, sessionView(t, sessionOwn, "s", 5))
	for _, rev := range []int{4, 5} {
		if ref, err := c.Apply([]byte(viewLine(sessionOwn, rev))); err != nil || ref != (Ref{}) {
			t.Errorf("revision %d の Apply = %+v, %v, want 読み直さない", rev, ref, err)
		}
	}
	if ref, err := c.Apply([]byte(viewLine(sessionOwn, 6))); err != nil || ref != (Ref{Session: sessionOwn, View: true}) {
		t.Errorf("新しい revision の Apply = %+v, %v, want 読み直し", ref, err)
	}
}

// PutView は持っている view より古い revision の view を捨てる (読み直しの応答が入れ違いに届いたとき)。
func TestCache_PutViewKeepsNewest(t *testing.T) {
	c := NewCache(map[string]*State{blockOwn: state(t, sessionOwn, psEmpty, processJSON(pidOwn, "claude", cwdOwn))})
	c.PutView(sessionOwn, sessionView(t, sessionOwn, "new", 7, [3]string{windowA, "w", blockOwn}))
	c.PutView(sessionOwn, sessionView(t, sessionOwn, "old", 6, [3]string{windowA, "w", blockOwn}))
	if got := c.Panes(nil)[0].Location.SessionLabel; got != "new" {
		t.Errorf("SessionLabel = %q, want new (古い revision で上書きしない)", got)
	}
}

// session_destroyed は view も捨てる。同じ ID の session の view が後で来たら読み直す。
func TestCache_SessionDestroyedDropsView(t *testing.T) {
	c := NewCache(nil)
	c.PutView(sessionOwn, sessionView(t, sessionOwn, "s", 9))
	if _, err := c.Apply([]byte(sessionDestroyedLine(sessionOwn))); err != nil {
		t.Fatal(err)
	}
	if ref, err := c.Apply([]byte(viewLine(sessionOwn, 1))); err != nil || ref != (Ref{Session: sessionOwn, View: true}) {
		t.Errorf("破棄の後の Apply = %+v, %v, want 読み直し", ref, err)
	}
}
