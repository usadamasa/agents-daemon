package rexcli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/usadamasa/agents-daemon/internal/multiplexer"
	"github.com/usadamasa/agents-daemon/internal/multiplexer/rexcli/blockstate"
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

// ctx を止めると、Watch の返す channel は events の子プロセスを刈り取ってから閉じる。daemon はこれを待ってから
// 終わるので、入れ替わりのたびに events の子が rex に繋がったまま残らない。
func TestWatch_DoneAfterChildReaped(t *testing.T) {
	f := newFakeRex(t)
	putOwnBlock(t, f)
	f.put(t, "events.jsonl", measured(t, measuredOct27, "events.jsonl"))
	f.put(t, "events-hold", "")
	c := f.client(staticResolver(nil))
	c.watchBackoff = time.Hour
	ctx, cancel := context.WithCancel(context.Background())
	done := c.Watch(ctx, (&logSink{}).logf)
	eventually(t, "同期済み", c.watch.synced)

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("ctx を止めても 2 秒で購読が終わらない (events の子が残る)")
	}
	if c.watch.synced() {
		t.Error("購読が終わった後も同期済みのまま")
	}
}

// clearCalls は calls.log を空にする。以降の rex の呼び出しだけを assertCalls で見る。
func (f *fakeRex) clearCalls(t *testing.T) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(f.dir, "calls.log"), nil, 0o600); err != nil {
		t.Fatalf("calls.log を空にできない: %v", err)
	}
}

// putOwnBlock は自分の session に shell の zsh が前景の block だけがある環境を置く。
func putOwnBlock(t *testing.T, f *fakeRex) {
	t.Helper()
	f.put(t, "ls.json", lsJSON(sessionOwn))
	f.put(t, "block-ls-"+sessionOwn+".json", blockLsJSON([2]string{blockOwn, blockstate.ShellFlavor}))
	f.putBlock(t, blockOwn, psEmpty, processJSON(pidOwnShell, "-zsh", cwdOwn))
}

// 同期した後の PaneList は rex を叩かずに cache から返す。stream の event は全列挙の後に当たる。
func TestWatch_PaneListFromCache(t *testing.T) {
	f := newFakeRex(t)
	putOwnBlock(t, f)
	f.put(t, "events.jsonl", measured(t, measuredOct27, "events.jsonl"))
	f.put(t, "events-hold", "")
	c := f.client(staticResolver(map[string]string{blockOwn: sidOwn}))

	startWatch(t, c)
	eventually(t, "同期済み", c.watch.synced)
	f.clearCalls(t)

	panes, err := c.PaneList(context.Background())
	if err != nil {
		t.Fatalf("PaneList() error = %v", err)
	}
	want := []multiplexer.Pane{{
		PaneID: blockOwn, CWD: cwdOwn, Agent: "claude", AgentStatus: multiplexer.AgentStatusWorking,
		AgentMessage: "Close probe block after its program exits", SessionID: sidOwn,
	}}
	if !slices.Equal(panes, want) {
		t.Errorf("PaneList() =\n%+v\nwant\n%+v", panes, want)
	}
	f.assertCalls(t)
}

// 購読は events --all --json を張り、最初の行を受けてから全列挙で cache を作る。stream の途中の block_created は
// その session に block ls を打ち、shell の block なら program_status と process で埋める (使い捨ての block は
// block ls に無いので埋めない)。
func TestWatch_Calls(t *testing.T) {
	f := newFakeRex(t)
	putOwnBlock(t, f)
	f.put(t, "events.jsonl", measured(t, measuredOct27, "events.jsonl"))
	f.put(t, "events-hold", "")
	c := f.client(staticResolver(nil))

	startWatch(t, c)
	eventually(t, "同期済み", c.watch.synced)
	eventually(t, "block_created の block ls", func() bool {
		return len(readLines(t, filepath.Join(f.dir, "calls.log"))) >= 6
	})
	f.assertCalls(t,
		"--autostart=false events --all --json",
		"--autostart=false ls --json",
		"--autostart=false block ls -s "+sessionOwn+" --json",
		"--autostart=false block call -b "+blockOwn+" program_status",
		"--autostart=false block call -b "+blockOwn+" process",
		"--autostart=false block ls -s "+sessionOwn+" --json",
	)
}

// 新しい session の最初の block には block_created が来ない (testdata の events-session-new.jsonl)。session_created で
// その session の shell の block を全部埋め、以降の event が当たる。
func TestWatch_NewSessionBlocks(t *testing.T) {
	f := newFakeRex(t)
	f.put(t, "ls.json", lsJSON(sessionA))
	f.put(t, "block-ls-"+sessionA+".json", blockLsJSON())
	f.put(t, "block-ls-"+sessionB+".json", blockLsJSON(
		[2]string{blockClaudeDone, blockstate.ShellFlavor},
		[2]string{blockNotShell, "com.superlogical.browser"},
	))
	f.putBlock(t, blockClaudeDone, psEmpty, processJSON(pidClaudeDone, "claude", "/b"))
	f.put(t, "events.jsonl", strings.Join([]string{
		`{"type":"session_created","payload":{"session":{"session_id":"` + sessionB + `","label":"new"}}}`,
		psChangedLine(sessionB, blockClaudeDone, "done", "できた", pidClaudeDone),
	}, "\n")+"\n")
	f.put(t, "events-hold", "")
	c := f.client(staticResolver(nil))

	startWatch(t, c)
	eventually(t, "新しい session の block が cache に入る", func() bool {
		panes, ok := c.watch.panes(nil)
		return ok && len(panes) == 1 && panes[0].AgentStatus == multiplexer.AgentStatusDone
	})
	panes, _ := c.watch.panes(nil)
	want := multiplexer.Pane{PaneID: blockClaudeDone, CWD: "/b", Agent: "claude", AgentStatus: multiplexer.AgentStatusDone, AgentMessage: "できた"}
	if panes[0] != want {
		t.Errorf("panes = %+v, want %+v", panes[0], want)
	}
}

// stream が切れたら未同期に戻り、理由をログに残す。未同期の間の PaneList は全列挙で返す。
func TestWatch_StreamEndFallsBackToListing(t *testing.T) {
	f := newFakeRex(t)
	putOwnBlock(t, f)
	f.put(t, "events.jsonl", measured(t, measuredOct27, "events.jsonl"))
	c := f.client(staticResolver(nil))

	sink := startWatch(t, c)
	eventually(t, "切断のログ", func() bool { return len(sink.snapshot()) > 0 })
	if c.watch.synced() {
		t.Error("stream が切れた後も同期済みのまま")
	}
	if got := sink.snapshot()[0]; !strings.Contains(got, "rex events の購読が切れ") {
		t.Errorf("log = %q, want 購読が切れた旨", got)
	}

	f.clearCalls(t)
	if _, err := c.PaneList(context.Background()); err != nil {
		t.Fatalf("PaneList() error = %v", err)
	}
	f.assertCalls(t,
		"--autostart=false ls --json",
		"--autostart=false block ls -s "+sessionOwn+" --json",
		"--autostart=false block call -b "+blockOwn+" program_status",
		"--autostart=false block call -b "+blockOwn+" process",
	)
}

// events を張れなければ (server が古い、落ちている) 同期せず、PaneList は全列挙のまま。
func TestWatch_CannotSubscribe(t *testing.T) {
	f := newFakeRex(t)
	putOwnBlock(t, f)
	c := f.client(staticResolver(nil))

	sink := startWatch(t, c)
	eventually(t, "失敗のログ", func() bool { return len(sink.snapshot()) > 0 })
	if _, ok := c.watch.panes(nil); ok {
		t.Error("購読できないのに cache を返した")
	}
	panes, err := c.PaneList(context.Background())
	if err != nil || len(panes) != 1 || panes[0].PaneID != blockOwn {
		t.Errorf("PaneList() = %+v, %v; want 全列挙の 1 件", panes, err)
	}
}

// Watch を呼ばない Client (doctor、inspect、simulate) は購読しない。
func TestWatch_NotStartedWithoutWatch(t *testing.T) {
	f := newFakeRex(t)
	putOwnBlock(t, f)
	c := f.client(staticResolver(nil))
	if _, err := c.PaneList(context.Background()); err != nil {
		t.Fatalf("PaneList() error = %v", err)
	}
	for _, line := range readLines(t, filepath.Join(f.dir, "calls.log")) {
		if strings.Contains(line, "events") {
			t.Errorf("Watch を呼んでいないのに events を張った: %s", line)
		}
	}
}
