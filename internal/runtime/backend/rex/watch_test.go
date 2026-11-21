package rex

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/usadamasa/agents-daemon/internal/multiplexer"
	"github.com/usadamasa/agents-daemon/internal/runtime/backend/rex/api/apitest"
	"github.com/usadamasa/agents-daemon/internal/runtime/backend/rex/api/control/controltest"
	"github.com/usadamasa/agents-daemon/internal/runtime/backend/rex/wire"
)

// logSink は Watch の logf が書いた行を集める。
type logSink struct {
	mu    sync.Mutex
	lines []string
}

func (s *logSink) logf(format string, args ...any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lines = append(s.lines, fmt.Sprintf(format, args...))
}

func (s *logSink) snapshot() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.lines)
}

// eventually は cond が true になるまで待つ。5 秒で諦める。
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("%s にならない", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// startWatch は Watch を始め、テストの終わりに止めて購読の終わりを待つ。張り直しの間隔は 1 時間にし、1 回目の購読だけを見る。
func startWatch(t *testing.T, c *execClient) *logSink {
	t.Helper()
	c.watchBackoff = time.Hour
	ctx, cancel := context.WithCancel(context.Background())
	sink := &logSink{}
	done := c.Watch(ctx, sink.logf)
	t.Cleanup(func() {
		cancel()
		<-done
	})
	return sink
}

// synced は購読が同期して PaneList が cache から返す状態か。
func synced(c *execClient) bool {
	_, ok := c.watch.Panes(nil)
	return ok
}

// emitLines は 1 行 1 event (`rex events --json` と同じ形) の lines を、attach_all した接続へ順に流す。
func emitLines(f *fakeAPI, lines string) {
	for _, line := range strings.Split(lines, "\n") {
		if line != "" {
			f.Protocol().Emit(json.RawMessage(line))
		}
	}
}

// ownBlockAPI は自分の session に shell の zsh が前景の block だけがある fake の API を返す。
func ownBlockAPI(t *testing.T) *fakeAPI {
	t.Helper()
	api := newFakeAPI(t)
	api.AddSession(sessionOwn, [2]string{blockOwn, wire.ShellFlavor})
	api.PutBlock(blockOwn, psEmpty, processJSON(pidOwnShell, "-zsh", cwdOwn))
	return api
}

// 購読は呼び出しと別の接続で attach_all (event_scope all) を打つ。block_event は event_scope all の接続にだけ届く。
// ctx を止めると、Watch の返す channel は購読の接続を close frame で閉じてから閉じる。daemon はこれを待ってから
// 終わるので、入れ替わりのたびに購読の client が rex に残らない。
func TestWatch_SubscribesOnOwnConnectionAndClosesIt(t *testing.T) {
	api := ownBlockAPI(t)
	c := api.client(staticResolver(nil))
	c.watchBackoff = time.Hour
	ctx, cancel := context.WithCancel(context.Background())
	done := c.Watch(ctx, (&logSink{}).logf)
	eventually(t, "同期済み", func() bool { return synced(c) })

	var scopes []string
	for _, p := range api.RequestsOf("session.attach_all") {
		var req struct {
			EventScope string `json:"event_scope"`
		}
		_ = json.Unmarshal(p, &req)
		scopes = append(scopes, req.EventScope)
	}
	slices.Sort(scopes)
	if !slices.Equal(scopes, []string{"all", "session"}) {
		t.Errorf("attach_all の event_scope = %v, want 購読の all と呼び出しの session の 2 本", scopes)
	}
	if n := api.Protocol().Connections(); n != 2 {
		t.Errorf("接続 = %d, want 購読と呼び出しの 2 本", n)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("ctx を止めても 2 秒で購読が終わらない")
	}
	if synced(c) {
		t.Error("購読が終わった後も同期済みのまま")
	}
	eventually(t, "購読の接続が閉じる", func() bool { return api.Protocol().Connections() == 1 })
}

// 同期した後の PaneList は rex を叩かずに cache から返す。同期の後に届いた event が cache に当たる。
func TestWatch_PaneListFromCache(t *testing.T) {
	api := ownBlockAPI(t)
	c := api.client(staticResolver(map[string]string{blockOwn: sidOwn}))

	startWatch(t, c)
	eventually(t, "同期済み", func() bool { return synced(c) })
	emitLines(api, measured(t, measuredOct27, "events.jsonl"))
	// stream の block_created、session_view_changed、session_created が打つ list_blocks と session.view を待ってから数える。
	eventually(t, "stream の event の list_blocks と session.view", func() bool { return len(api.Calls()) >= 10 })
	before := len(api.Calls())

	panes, err := c.PaneList(context.Background())
	if err != nil {
		t.Fatalf("PaneList() error = %v", err)
	}
	want := []multiplexer.Pane{{
		PaneID: blockOwn, CWD: cwdOwn, PID: pidOwn, Agent: "claude", AgentStatus: multiplexer.AgentStatusWorking,
		AgentMessage: "Close probe block after its program exits", SessionID: sidOwn,
		Location: multiplexer.Location{SessionID: sessionOwn, SessionLabel: apitest.DefaultSessionLabel},
	}}
	if !slices.Equal(panes, want) {
		t.Errorf("PaneList() =\n%+v\nwant\n%+v", panes, want)
	}
	if after := api.Calls()[before:]; len(after) != 0 {
		t.Errorf("cache から返すはずが API を叩いた: %v", after)
	}
}

// 購読を張ってから全列挙で cache を作る。stream の途中の block_created はその session に list_blocks を打ち、shell の
// block なら program_status と process で埋める (使い捨ての block は list_blocks に無いので埋めない)。
func TestWatch_Calls(t *testing.T) {
	api := ownBlockAPI(t)
	c := api.client(staticResolver(nil))

	startWatch(t, c)
	eventually(t, "同期済み", func() bool { return synced(c) })
	emitLines(api, measured(t, measuredOct27, "events.jsonl"))
	eventually(t, "block_created と session_created の list_blocks と session.view", func() bool { return len(api.Calls()) >= 10 })
	api.assertCalls(t,
		"session.list",
		"session.list_blocks "+sessionOwn,
		"session.view "+sessionOwn,
		"program_status "+blockOwn,
		"process "+blockOwn,
		// 全列挙の後に購読の接続で打つ同期。応答の seq までの event を当ててから公開する。
		"server.version",
		"session.list_blocks "+sessionOwn,
		// 実測の stream の session_view_changed (revision 17) は全列挙の view (revision 1) より新しいので読み直す。
		"session.view "+sessionOwn,
		// 実測の stream の session_created。fake にその session は無いので session_not_found で読み飛ばす。
		"session.list_blocks session:01a17d51-a67b-7c38-bd8e-3265c5f30a95",
		"session.view session:01a17d51-a67b-7c38-bd8e-3265c5f30a95",
	)
}

// window の改名は session_view_changed で届き、session.view を読み直して PaneList の window の label が変わる。
// rex-premise: preview.30 api window の改名は window_label_changed の後に session_view_changed を流す (2026-11-20 に実測)
func TestWatch_ViewChangedRelabels(t *testing.T) {
	api := ownBlockAPI(t)
	view := func(revision int, label string) string {
		return `{"revision":` + strconv.Itoa(revision) + `,"session_id":"` + sessionOwn + `","label":"work","windows":[{"window_id":"` + windowA +
			`","label":"` + label + `","layers":[{"kind":"tiled","blocks":[{"block_id":"` + blockOwn + `"}]}]}]}`
	}
	api.SetView(sessionOwn, view(2, "peer-a"))
	c := api.client(staticResolver(nil))

	startWatch(t, c)
	eventually(t, "同期済み", func() bool { return synced(c) })
	label := func() string {
		panes, _ := c.watch.Panes(nil)
		if len(panes) != 1 {
			return ""
		}
		return panes[0].Location.WindowLabel
	}
	if got := label(); got != "peer-a" {
		t.Fatalf("同期の直後の window の label = %q, want peer-a", got)
	}
	api.SetView(sessionOwn, view(3, "peer-b"))
	emitLines(api, strings.Join([]string{
		`{"type":"session_event","payload":{"session_id":"` + sessionOwn + `","event":{"type":"window_label_changed","payload":{"window_id":"` + windowA + `","label":"peer-b"}}}}`,
		`{"type":"session_event","payload":{"session_id":"` + sessionOwn + `","event":{"type":"session_view_changed","payload":{"revision":3}}}}`,
	}, "\n"))
	eventually(t, "改名した window の label", func() bool { return label() == "peer-b" })
}

// session_view_changed の読み直しが失敗したら、その理由を pane の LocationReason に載せる。読み直せたら消す。
func TestWatch_ViewReadFailureSetsReason(t *testing.T) {
	api := ownBlockAPI(t)
	c := api.client(staticResolver(nil))
	startWatch(t, c)
	eventually(t, "同期済み", func() bool { return synced(c) })
	reason := func() string {
		panes, _ := c.watch.Panes(nil)
		if len(panes) != 1 {
			return "<pane なし>"
		}
		return panes[0].LocationReason
	}
	if got := reason(); got != "" {
		t.Fatalf("同期の直後の LocationReason = %q, want 空", got)
	}
	changed := func(revision int) string {
		return `{"type":"session_event","payload":{"session_id":"` + sessionOwn + `","event":{"type":"session_view_changed","payload":{"revision":` +
			strconv.Itoa(revision) + `}}}}`
	}
	api.SetView(sessionOwn, `{"revision":5,"session_id":"`+sessionOwn+`","label":"work"}`)
	emitLines(api, changed(5))
	eventually(t, "読み直しの失敗の理由", func() bool { return strings.Contains(reason(), "windows") })

	api.SetView(sessionOwn, `{"revision":6,"session_id":"`+sessionOwn+`","label":"work","windows":[]}`)
	emitLines(api, changed(6))
	eventually(t, "読み直せたら理由が消える", func() bool { return reason() == "" })
}

// 全列挙の途中に届いた event は、全列挙の後に順に当て直す。全列挙が読んだ後の変化を落とさない。
func TestWatch_EventDuringSnapshotIsApplied(t *testing.T) {
	api := ownBlockAPI(t)
	// 全列挙が process を読む間に、block の Claude Code が done を報告する。
	api.Protocol().Handle(apitest.TerminalMethod+"process", func(json.RawMessage) (any, error) {
		emitLines(api, psChangedLine(sessionOwn, blockOwn, "done", "できた", pidOwn))
		return json.RawMessage(processJSON(pidOwn, "claude", cwdOwn)), nil
	})
	c := api.client(staticResolver(nil))

	startWatch(t, c)
	eventually(t, "全列挙の途中の event が cache に当たる", func() bool {
		panes, ok := c.watch.Panes(nil)
		return ok && len(panes) == 1 && panes[0].AgentStatus == multiplexer.AgentStatusDone
	})
}

// 全列挙が読んだ変化の event が全列挙の応答より後に届いても、同期の前に当てる。全列挙の途中に working → done と
// 変わり、全列挙は done を読み、done の event だけ遅れて届く。同期の後の PaneList は一度も working (全列挙より古い
// 状態) を返さない。
func TestWatch_SyncCoversEventsTheSnapshotSaw(t *testing.T) {
	api := ownBlockAPI(t)
	api.Put("program_status", blockOwn, `{"records":[{"state":"done","app":"claude-code","msg":"できた","owner":{"pid":`+strconv.Itoa(pidOwn)+`}}]}`)
	api.Protocol().Handle(apitest.TerminalMethod+"process", func(json.RawMessage) (any, error) {
		emitLines(api, psChangedLine(sessionOwn, blockOwn, "working", "作業中", pidOwn))
		api.Protocol().EmitDelayed(json.RawMessage(psChangedLine(sessionOwn, blockOwn, "done", "できた", pidOwn)), 300*time.Millisecond)
		return json.RawMessage(processJSON(pidOwn, "claude", cwdOwn)), nil
	})
	c := api.client(staticResolver(nil))

	startWatch(t, c)
	eventually(t, "同期済み", func() bool { return synced(c) })
	deadline := time.Now().Add(600 * time.Millisecond)
	for time.Now().Before(deadline) {
		panes, ok := c.watch.Panes(nil)
		if ok && (len(panes) != 1 || panes[0].AgentStatus != multiplexer.AgentStatusDone) {
			t.Fatalf("同期の後に全列挙より古い状態を返した: %+v", panes)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// 新しい session の最初の block には block_created が来ない (testdata の events-session-new.jsonl、2026-11-18 に API の
// event でも同じことを確かめた)。session_created でその session の shell の block を全部埋め、以降の event が当たる。
func TestWatch_NewSessionBlocks(t *testing.T) {
	api := newFakeAPI(t)
	api.AddSession(sessionA)
	// session B は全列挙の後にできた session なので、session.list には出さない。
	api.AddUnlisted(sessionB,
		[2]string{blockClaudeDone, wire.ShellFlavor},
		[2]string{blockNotShell, "com.superlogical.browser"},
	)
	api.PutBlock(blockClaudeDone, psEmpty, processJSON(pidClaudeDone, "claude", "/b"))
	c := api.client(staticResolver(nil))

	startWatch(t, c)
	eventually(t, "同期済み", func() bool { return synced(c) })
	emitLines(api, strings.Join([]string{
		`{"type":"session_created","payload":{"session":{"session_id":"` + sessionB + `","label":"new"}}}`,
		psChangedLine(sessionB, blockClaudeDone, "done", "できた", pidClaudeDone),
	}, "\n"))
	eventually(t, "新しい session の block が cache に入る", func() bool {
		panes, ok := c.watch.Panes(nil)
		return ok && len(panes) == 1 && panes[0].AgentStatus == multiplexer.AgentStatusDone
	})
	want := multiplexer.Pane{PaneID: blockClaudeDone, CWD: "/b", PID: pidClaudeDone, Agent: "claude", AgentStatus: multiplexer.AgentStatusDone, AgentMessage: "できた",
		Location: multiplexer.Location{SessionID: sessionB, SessionLabel: apitest.DefaultSessionLabel}}
	eventually(t, "新しい session の view が cache に入る", func() bool {
		panes, _ := c.watch.Panes(nil)
		return len(panes) == 1 && panes[0].Location.SessionID == sessionB
	})
	panes, _ := c.watch.Panes(nil)
	if panes[0] != want {
		t.Errorf("panes = %+v, want %+v", panes[0], want)
	}
}

// session_created の後の Fill が読んだ変化の event が Fill の応答より後に届いても、埋めた block を公開する前に当てる。
// Fill の途中に working → done と変わり、Fill は done を読み、done の event だけ遅れて届く。埋めた後の PaneList は
// 一度も working (Fill より古い状態) を返さない。
func TestWatch_FillCoversEventsItSaw(t *testing.T) {
	api := newFakeAPI(t)
	api.AddSession(sessionA)
	api.AddUnlisted(sessionB, [2]string{blockClaudeDone, wire.ShellFlavor})
	api.Put("program_status", blockClaudeDone, `{"records":[{"state":"done","app":"claude-code","msg":"できた","owner":{"pid":`+strconv.Itoa(pidClaudeDone)+`}}]}`)
	api.Protocol().Handle(apitest.TerminalMethod+"process", func(json.RawMessage) (any, error) {
		emitLines(api, psChangedLine(sessionB, blockClaudeDone, "working", "作業中", pidClaudeDone))
		api.Protocol().EmitDelayed(json.RawMessage(psChangedLine(sessionB, blockClaudeDone, "done", "できた", pidClaudeDone)), 300*time.Millisecond)
		return json.RawMessage(processJSON(pidClaudeDone, "claude", "/b")), nil
	})
	c := api.client(staticResolver(nil))

	startWatch(t, c)
	eventually(t, "同期済み", func() bool { return synced(c) })
	emitLines(api, `{"type":"session_created","payload":{"session":{"session_id":"`+sessionB+`","label":"new"}}}`)
	eventually(t, "新しい session の block が cache に入る", func() bool {
		panes, _ := c.watch.Panes(nil)
		return len(panes) == 1
	})
	deadline := time.Now().Add(600 * time.Millisecond)
	for time.Now().Before(deadline) {
		panes, _ := c.watch.Panes(nil)
		if len(panes) != 1 || panes[0].AgentStatus != multiplexer.AgentStatusDone {
			t.Fatalf("Fill の後に Fill より古い状態を返した: %+v", panes)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// Fill の後に当てる event の中の block_created も、埋めてから同期の seq まで当てて公開する。session_created の Fill の
// 途中に 2 つ目の block ができ、その Fill の途中に working → done と変わって done の event だけ遅れて届く。2 つ目の
// block も一度も working を返さず、同期は全列挙と 2 回の Fill の 3 回打つ。
func TestWatch_FillCoversEventsItSawWhenNested(t *testing.T) {
	api := newFakeAPI(t)
	api.AddSession(sessionA)
	api.AddUnlisted(sessionB, [2]string{blockClaudeDone, wire.ShellFlavor})
	done := func(pid int) string {
		return `{"records":[{"state":"done","app":"claude-code","msg":"できた","owner":{"pid":` + strconv.Itoa(pid) + `}}]}`
	}
	api.Put("program_status", blockClaudeDone, done(pidClaudeDone))
	api.Put("program_status", blockOwn, done(pidOwn))
	var calls atomic.Int32
	api.Protocol().Handle(apitest.TerminalMethod+"process", func(json.RawMessage) (any, error) {
		// 1 回目は session_created の Fill、2 回目は入れ子の block_created の Fill。
		if calls.Add(1) == 1 {
			api.SetBlocks(sessionB, [2]string{blockClaudeDone, wire.ShellFlavor}, [2]string{blockOwn, wire.ShellFlavor})
			emitLines(api, `{"type":"session_event","payload":{"session_id":"`+sessionB+`","event":{"type":"block_created","payload":{"block_id":"`+blockOwn+`"}}}}`)
			return json.RawMessage(processJSON(pidClaudeDone, "claude", "/b")), nil
		}
		emitLines(api, psChangedLine(sessionB, blockOwn, "working", "作業中", pidOwn))
		api.Protocol().EmitDelayed(json.RawMessage(psChangedLine(sessionB, blockOwn, "done", "できた", pidOwn)), 300*time.Millisecond)
		return json.RawMessage(processJSON(pidOwn, "claude", cwdOwn)), nil
	})
	c := api.client(staticResolver(nil))

	startWatch(t, c)
	eventually(t, "同期済み", func() bool { return synced(c) })
	emitLines(api, `{"type":"session_created","payload":{"session":{"session_id":"`+sessionB+`","label":"new"}}}`)
	eventually(t, "2 つの block が cache に入る", func() bool {
		panes, _ := c.watch.Panes(nil)
		return len(panes) == 2
	})
	deadline := time.Now().Add(600 * time.Millisecond)
	for time.Now().Before(deadline) {
		panes, _ := c.watch.Panes(nil)
		if len(panes) != 2 || panes[0].AgentStatus != multiplexer.AgentStatusDone || panes[1].AgentStatus != multiplexer.AgentStatusDone {
			t.Fatalf("入れ子の Fill の後に Fill より古い状態を返した: %+v", panes)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if n := len(api.RequestsOf("server.version")); n != 3 {
		t.Errorf("server.version = %d 回, want 全列挙と 2 回の Fill の後の 3 回", n)
	}
}

// 購読の接続が切れたら未同期に戻り、理由をログに残す。未同期の間の PaneList は全列挙で返す。
func TestWatch_DisconnectFallsBackToListing(t *testing.T) {
	api := ownBlockAPI(t)
	c := api.client(staticResolver(nil))

	sink := startWatch(t, c)
	eventually(t, "同期済み", func() bool { return synced(c) })
	api.Protocol().CloseConnections()
	eventually(t, "切断のログ", func() bool { return len(sink.snapshot()) > 0 })
	if synced(c) {
		t.Error("購読が切れた後も同期済みのまま")
	}
	if got := sink.snapshot()[0]; !strings.Contains(got, "rex の event の購読が切れ") {
		t.Errorf("log = %q, want 購読が切れた旨", got)
	}

	before := len(api.Calls())
	if _, err := c.PaneList(context.Background()); err != nil {
		t.Fatalf("PaneList() error = %v", err)
	}
	want := []string{"session.list", "session.list_blocks " + sessionOwn, "session.view " + sessionOwn, "program_status " + blockOwn, "process " + blockOwn}
	if got := api.Calls()[before:]; !slices.Equal(got, want) {
		t.Errorf("全列挙の calls = %v, want %v", got, want)
	}
}

// 購読を張れなければ (server が古い、event_scope all を断る) 同期せず、PaneList は全列挙のまま。
func TestWatch_CannotSubscribe(t *testing.T) {
	api := ownBlockAPI(t)
	api.Protocol().Handle("session.attach_all", func(p json.RawMessage) (any, error) {
		if strings.Contains(string(p), `"all"`) {
			return nil, &controltest.Error{Code: "invalid_argument", Message: "unknown event_scope", Status: 400}
		}
		return map[string]any{}, nil
	})
	c := api.client(staticResolver(nil))

	sink := startWatch(t, c)
	eventually(t, "失敗のログ", func() bool { return len(sink.snapshot()) > 0 })
	if synced(c) {
		t.Error("購読できないのに cache を返した")
	}
	panes, err := c.PaneList(context.Background())
	if err != nil || len(panes) != 1 || panes[0].PaneID != blockOwn {
		t.Errorf("PaneList() = %+v, %v; want 全列挙の 1 件", panes, err)
	}
}

// Watch を呼ばない Client (doctor、inspect、simulate) は購読しない。
func TestWatch_NotStartedWithoutWatch(t *testing.T) {
	api := ownBlockAPI(t)
	c := api.client(staticResolver(nil))
	if _, err := c.PaneList(context.Background()); err != nil {
		t.Fatalf("PaneList() error = %v", err)
	}
	for _, p := range api.RequestsOf("session.attach_all") {
		if strings.Contains(string(p), `"all"`) {
			t.Errorf("Watch を呼んでいないのに購読した: %s", p)
		}
	}
}
