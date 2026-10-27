package blockstate

import (
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/usadamasa/agents-daemon/internal/multiplexer"
)

// session と block は 2026-10-10 の実測の ID。
const (
	sessionA           = "session:01a12439-0839-7829-a4cb-aeac453570c1"
	sessionB           = "session:01a1250f-c100-71e0-aed0-4a433569cf00"
	blockClaudeWorking = "block:01a12439-083a-74f9-9827-49c3478b2469"
	blockClaudeDone    = "block:01a12540-cd47-7943-a503-91a06c58fab5"
)

// 2026-10-27 の events.jsonl に出る ID。
const (
	sessionOwn  = "session:01a17d3f-df41-7910-a1ba-34bf3c9384f2"
	blockOwn    = "block:01a17d3f-df41-7a0a-b43a-445fabff2f4f"
	blockProbe  = "block:01a17d4c-daa7-7d94-b78f-1a7a0b517135"
	pidOwn      = 79724
	pidOwnShell = 79054
	sidOwn      = "8c798e60-d1c2-4967-b62a-a1b2a4be9fc3"
	cwdOwn      = "/Users/usadamasa/src/github.com/usadamasa/agents-daemon"
)

// 以下は events --all --json の行を 2026-10-27 の実測の形で組む。

func blockEventLine(session, block, name, payload string) string {
	return `{"type":"session_event","payload":{"session_id":"` + session + `","event":{"type":"block_event","payload":{"block_id":"` + block +
		`","creator_name":"com.superlogical.terminal","name":"` + name + `","payload":` + payload + `}}}}`
}

func psChangedLine(session, block, id, state, msg string, pid int) string {
	record := `{"state":"` + state + `","app":"claude-code","effective_app":"claude-code","msg":"` + msg +
		`","updated_at":"2026-10-27T21:19:18.848469+09:00","owner":{"pid":` + strconv.Itoa(pid) + `,"name":"2.1.311"}}`
	if id != "" {
		record = `{"id":"` + id + `",` + record[1:]
	}
	return blockEventLine(session, block, "program_status_changed", `{"id":"`+id+`","record":`+record+`,"reason":"report"}`)
}

func psRemovedLine(session, block, reason string, ids ...string) string {
	quoted := make([]string, 0, len(ids))
	for _, id := range ids {
		quoted = append(quoted, strconv.Quote(id))
	}
	return blockEventLine(session, block, "program_status_removed", `{"ids":[`+strings.Join(quoted, ",")+`],"reason":"`+reason+`"}`)
}

func procChangedLine(session, block string, generation, pid int, argv0, cwd string) string {
	return blockEventLine(session, block, "process_changed", `{"generation":`+strconv.Itoa(generation)+
		`,"foreground":{"pid":`+strconv.Itoa(pid)+`,"user_id":501,"user":"usadamasa","name":"x","path":"/x","invoked_path":"/x","argv0":"`+argv0+`","cwd":"`+cwd+`"}}`)
}

func sessionDestroyedLine(session string) string {
	return `{"type":"session_destroyed","payload":{"session_id":"` + session + `"}}`
}

// state は program_status と process の応答 (JSON) から block の cache を作る。
func state(t *testing.T, session, programStatus, process string) *State {
	t.Helper()
	st := &State{Session: session}
	if err := json.Unmarshal([]byte(programStatus), &st.ProgramStatus); err != nil {
		t.Fatalf("program_status の parse に失敗: %v", err)
	}
	if err := json.Unmarshal([]byte(process), &st.Process); err != nil {
		t.Fatalf("process の parse に失敗: %v", err)
	}
	return st
}

// applyAll は行を順に当て、block_created が求めた block の一覧を返す。
func applyAll(t *testing.T, c *Cache, lines []string) []Ref {
	t.Helper()
	var created []Ref
	for i, line := range lines {
		ref, err := c.Apply([]byte(line))
		if err != nil {
			t.Fatalf("apply(%d 行目) error = %v", i+1, err)
		}
		if ref != (Ref{}) {
			created = append(created, ref)
		}
	}
	return created
}

// 実測の stream を頭から当てる。自分の block は shell の zsh が前景の snapshot から始まり、Claude の前景と
// working の record が届く。使い捨ての block は作られ、record を出し、終わって閉じる。
func TestCache_ReplaysMeasuredStream(t *testing.T) {
	c := NewCache(map[string]*State{
		blockOwn: state(t, sessionOwn, psEmpty, processJSON(pidOwnShell, "-zsh", cwdOwn)),
	})
	lines := strings.Split(strings.TrimSpace(measured(t, measuredOct27, "events.jsonl")), "\n")

	var created []Ref
	for i, line := range lines {
		ref, err := c.Apply([]byte(line))
		if err != nil {
			t.Fatalf("apply(%d 行目) error = %v", i+1, err)
		}
		if ref != (Ref{}) {
			created = append(created, ref)
			// block ls と program_status / process で埋めた直後の形 (前景はまだ無い)。新しい session は block を持たない。
			if ref.Block != "" {
				c.Put(ref, state(t, ref.Session, psEmpty, `{"last_exit":null,"foreground":null}`))
			}
		}
	}

	wantRefs := []Ref{{Session: sessionOwn, Block: blockProbe}, {Session: "session:01a17d51-a67b-7c38-bd8e-3265c5f30a95"}}
	if want := wantRefs; !slices.Equal(created, want) {
		t.Errorf("block_created が求めた block = %+v, want %+v", created, want)
	}
	got := c.Panes(map[string]string{blockOwn: sidOwn})
	want := []multiplexer.Pane{{
		PaneID: blockOwn, CWD: cwdOwn, Agent: "claude", AgentStatus: multiplexer.AgentStatusWorking,
		AgentMessage: "Close probe block after its program exits", SessionID: sidOwn,
	}}
	if !slices.Equal(got, want) {
		t.Errorf("panes() =\n%+v\nwant\n%+v (閉じた block は消え、自分の block は Claude の working)", got, want)
	}
}

// 全列挙の snapshot が stream の途中の event より新しくても、積んだ event を順に当て直せば最新に追いつく。
// removed がそのまま snapshot の record を消しても、その record を作った event が後ろにある。
func TestCache_ReplayOverNewerSnapshot(t *testing.T) {
	tests := []struct {
		name     string
		snapshot string
		lines    []string
		want     multiplexer.AgentStatus
		wantMsg  string
	}{
		{
			"removed の後に作られた record が snapshot にある",
			`{"records":[{"state":"done","app":"claude-code","msg":"後","owner":{"pid":79724,"name":"2.1.311"}}]}`,
			[]string{
				psChangedLine(sessionOwn, blockOwn, "", "working", "前", pidOwn),
				psRemovedLine(sessionOwn, blockOwn, "foreground", ""),
				psChangedLine(sessionOwn, blockOwn, "", "done", "後", pidOwn),
			},
			multiplexer.AgentStatusDone, "後",
		},
		{
			"最後の event が removed なら record は残らない",
			psEmpty,
			[]string{
				psChangedLine(sessionOwn, blockOwn, "", "working", "前", pidOwn),
				psRemovedLine(sessionOwn, blockOwn, "child_exited", ""),
			},
			multiplexer.AgentStatusUnknown, "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := NewCache(map[string]*State{
				blockOwn: state(t, sessionOwn, tt.snapshot, processJSON(pidOwn, "claude", cwdOwn)),
			})
			applyAll(t, c, tt.lines)
			got := c.Panes(nil)
			if len(got) != 1 || got[0].AgentStatus != tt.want || got[0].AgentMessage != tt.wantMsg {
				t.Errorf("panes() = %+v, want 1 件で agent_status=%s msg=%q", got, tt.want, tt.wantMsg)
			}
		})
	}
}

// subagent の record は id で root と並び、removed の ids で個別に消える。
func TestCache_SubagentRecord(t *testing.T) {
	c := NewCache(map[string]*State{
		blockOwn: state(t, sessionOwn, psEmpty, processJSON(pidOwn, "claude", cwdOwn)),
	})
	applyAll(t, c, []string{
		psChangedLine(sessionOwn, blockOwn, "", "working", "本体", pidOwn),
		psChangedLine(sessionOwn, blockOwn, "a01b73b4e3800b31f", "blocked", "subagent", pidOwn),
		psRemovedLine(sessionOwn, blockOwn, "clear", "a01b73b4e3800b31f"),
	})
	got := c.Panes(nil)
	if len(got) != 1 || got[0].AgentStatus != multiplexer.AgentStatusWorking || got[0].AgentMessage != "本体" {
		t.Errorf("panes() = %+v, want root の working だけが残る", got)
	}
	if n := len(c.blocks[blockOwn].ProgramStatus.Records); n != 1 {
		t.Errorf("records = %d 件, want 1 (subagent の record は消える)", n)
	}
}

// process_changed は前景を置き換える。Claude が終わって shell に戻ると、残った record は owner が合わず使わない。
func TestCache_ForegroundChange(t *testing.T) {
	c := NewCache(map[string]*State{
		blockOwn: state(t, sessionOwn, psEmpty, processJSON(pidOwnShell, "-zsh", cwdOwn)),
	})
	applyAll(t, c, []string{
		procChangedLine(sessionOwn, blockOwn, 1, pidOwn, "claude", cwdOwn),
		psChangedLine(sessionOwn, blockOwn, "", "done", "終わり", pidOwn),
	})
	if got := c.Panes(nil); len(got) != 1 || got[0].AgentStatus != multiplexer.AgentStatusDone {
		t.Fatalf("panes() = %+v, want Claude の done", got)
	}
	applyAll(t, c, []string{procChangedLine(sessionOwn, blockOwn, 2, pidOwnShell, "-zsh", "/elsewhere")})
	got := c.Panes(nil)
	want := []multiplexer.Pane{{PaneID: blockOwn, CWD: "/elsewhere", AgentStatus: multiplexer.AgentStatusUnknown}}
	if !slices.Equal(got, want) {
		t.Errorf("panes() = %+v, want %+v (done の record は残るが前景の shell の pane)", got, want)
	}
}

// session が消えたら、その session の block だけを落とす。
func TestCache_SessionDestroyed(t *testing.T) {
	c := NewCache(map[string]*State{
		blockClaudeWorking: state(t, sessionA, psEmpty, processJSON(pidClaudeWorking, "claude", "/a")),
		blockClaudeDone:    state(t, sessionB, psEmpty, processJSON(pidClaudeDone, "claude", "/b")),
	})
	applyAll(t, c, []string{sessionDestroyedLine(sessionA)})
	got := c.Panes(nil)
	if len(got) != 1 || got[0].PaneID != blockClaudeDone {
		t.Errorf("panes() = %+v, want session B の block だけ", got)
	}
}

// cache に無い block (shell 以外、まだ埋めていない) の event は読み捨てる。
func TestCache_IgnoresUnknownBlock(t *testing.T) {
	c := NewCache(nil)
	applyAll(t, c, []string{
		psChangedLine(sessionOwn, blockProbe, "", "working", "x", pidOwn),
		procChangedLine(sessionOwn, blockProbe, 1, pidOwn, "claude", "/x"),
	})
	if got := c.Panes(nil); len(got) != 0 {
		t.Errorf("panes() = %+v, want 空", got)
	}
}

// panes は block ID の順に並べる (map の順に依らない)。
func TestCache_PanesSorted(t *testing.T) {
	c := NewCache(map[string]*State{
		blockClaudeDone:    state(t, sessionB, psEmpty, processJSON(pidShell, "-zsh", "/b")),
		blockClaudeWorking: state(t, sessionA, psEmpty, processJSON(pidShell, "-zsh", "/a")),
	})
	got := c.Panes(nil)
	if len(got) != 2 || got[0].PaneID != blockClaudeWorking || got[1].PaneID != blockClaudeDone {
		t.Errorf("panes() = %+v, want block ID の順", got)
	}
}

// 新しい session の最初の block には block_created が来ない (実測)。session_created は block を空にした Ref で、
// その session の block を全部埋めるよう求める。埋める前に届いたその block の event は読み捨てる。
func TestCache_SessionCreated(t *testing.T) {
	const (
		sessionNew = "session:01a17d75-e4ee-7ee4-b406-edd2eeeab136"
		blockNew   = "block:01a17d75-e4ef-70af-9136-32071c27666e"
	)
	c := NewCache(nil)
	lines := strings.Split(strings.TrimSpace(measured(t, measuredOct27, "events-session-new.jsonl")), "\n")
	if got := applyAll(t, c, lines[:1]); !slices.Equal(got, []Ref{{Session: sessionNew}}) {
		t.Fatalf("session_created の Ref = %+v, want block の無い session だけの Ref", got)
	}
	c.Put(Ref{Session: sessionNew, Block: blockNew}, state(t, sessionNew, psEmpty, processJSON(pidShell, "/bin/zsh", "/Users/usadamasa")))
	applyAll(t, c, lines[1:3])
	if got := c.Panes(nil); len(got) != 1 || got[0].PaneID != blockNew {
		t.Fatalf("panes() = %+v, want 新しい session の block", got)
	}
	applyAll(t, c, lines[3:])
	if got := c.Panes(nil); len(got) != 0 {
		t.Errorf("panes() = %+v, want block_closed で空", got)
	}
}

// Relevant は実測の stream のうち Apply が読む行だけを通す。client の出入り、window、view の行は落とす。
func TestRelevant(t *testing.T) {
	var kept []string
	for line := range strings.SplitSeq(strings.TrimSpace(measured(t, measuredOct27, "events.jsonl")), "\n") {
		if !Relevant([]byte(line)) {
			continue
		}
		var l streamLine
		if err := json.Unmarshal([]byte(line), &l); err != nil {
			t.Fatal(err)
		}
		kind := l.Type
		if l.Payload.Event != nil {
			kind = l.Payload.Event.Type
		}
		kept = append(kept, kind)
	}
	want := []string{
		"block_event", "block_event", // 自分の block の process_changed と program_status_changed
		"block_created", "block_event", "block_event", "block_event", "block_event", "block_event", "block_event",
		"block_closed", "session_created", "session_destroyed",
	}
	if !slices.Equal(kept, want) {
		t.Errorf("通した行 = %v, want %v", kept, want)
	}
	if !Relevant([]byte("not json")) {
		t.Error("壊れた行を落とした (Apply に error を出させるべき)")
	}
}

func TestCache_BrokenLineIsError(t *testing.T) {
	// payload を読むのは cache にある block の event だけなので、block を置いておく。
	c := NewCache(map[string]*State{
		blockOwn: state(t, sessionOwn, psEmpty, processJSON(pidOwn, "claude", cwdOwn)),
	})
	if _, err := c.Apply([]byte("not json")); err == nil {
		t.Error("apply(壊れた行) error = nil, want error")
	}
	if _, err := c.Apply([]byte(blockEventLine(sessionOwn, blockOwn, "program_status_changed", `"not an object"`))); err == nil {
		t.Error("apply(payload が object でない) error = nil, want error")
	}
}
