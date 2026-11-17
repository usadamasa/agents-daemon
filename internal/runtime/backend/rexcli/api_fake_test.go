package rexcli

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/usadamasa/agents-daemon/internal/runtime/backend/rexcli/cli"
	"github.com/usadamasa/agents-daemon/internal/runtime/backend/rexcli/control/controltest"
)

// terminalMethod は block の method の request の method 名。
const terminalMethod = "com.superlogical.terminal."

// fakeAPI は controltest の fake server に、session と block の状態から応答を組む handler を載せたもの。
// 状態に無い session は session_not_found、無い block の method は not_found で答える (実機と同じ)。
type fakeAPI struct {
	srv *controltest.Server

	mu       sync.Mutex
	sessions []string
	// blocks は session ごとの block と flavor。key の無い session は list_blocks が session_not_found になる。
	blocks map[string][][2]string
	// results は "<method の短い名前>-<block>" の応答の JSON。format だけは画面の文字列。
	results map[string]string
	// failing は "<method の短い名前>-<block>" か "list_blocks-<session>" で、internal の失敗を返す。
	failing map[string]bool
	status  string
}

func newFakeAPI(t *testing.T) *fakeAPI {
	t.Helper()
	f := &fakeAPI{srv: controltest.New(t), blocks: map[string][][2]string{}, results: map[string]string{}, failing: map[string]bool{}}
	f.srv.Handle("server.status", func(json.RawMessage) (any, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.status == "" {
			return nil, &controltest.Error{Code: "internal", Message: "server error", Status: 500}
		}
		return json.RawMessage(f.status), nil
	})
	f.srv.Handle("session.list", func(json.RawMessage) (any, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		sessions := make([]map[string]string, 0, len(f.sessions))
		for _, s := range f.sessions {
			sessions = append(sessions, map[string]string{"session_id": s, "label": "shy-alcove"})
		}
		return map[string]any{"sessions": sessions}, nil
	})
	f.srv.Handle("session.list_blocks", func(p json.RawMessage) (any, error) {
		var req struct {
			SessionID string `json:"session_id"`
		}
		_ = json.Unmarshal(p, &req)
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.failing["list_blocks-"+req.SessionID] {
			return nil, &controltest.Error{Code: "internal", Message: "server error", Status: 500}
		}
		blocks, ok := f.blocks[req.SessionID]
		if !ok {
			return nil, controltest.SessionNotFound(req.SessionID)
		}
		out := make([]map[string]string, 0, len(blocks))
		for _, b := range blocks {
			out = append(out, map[string]string{"block_id": b[0], "label": "shell", "creator_name": "com.superlogical.terminal", "flavor": b[1], "placement": "placed"})
		}
		return map[string]any{"blocks": out}, nil
	})
	f.srv.Handle("target.resolve", func(p json.RawMessage) (any, error) {
		var req struct {
			Kinds   []string `json:"kinds"`
			Queries []string `json:"queries"`
		}
		_ = json.Unmarshal(p, &req)
		f.mu.Lock()
		defer f.mu.Unlock()
		results := make([]map[string]any, 0, len(req.Queries))
		for _, q := range req.Queries {
			r := map[string]any{"query": q, "status": "not_found"}
			for session, blocks := range f.blocks {
				for _, b := range blocks {
					if b[0] == q {
						r = map[string]any{"query": q, "status": "resolved", "target": map[string]any{"kind": "block", "session_id": session, "block_id": q, "label": "shell"}}
					}
				}
			}
			results = append(results, r)
		}
		return map[string]any{"results": results}, nil
	})
	for _, name := range []string{"program_status", "process", "size", "modes", "format"} {
		f.srv.Handle(terminalMethod+name, func(p json.RawMessage) (any, error) {
			var req struct {
				BlockID string `json:"block_id"`
			}
			_ = json.Unmarshal(p, &req)
			key := name + "-" + req.BlockID
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.failing[key] {
				return nil, &controltest.Error{Code: "internal", Message: "server error", Status: 500}
			}
			res, ok := f.results[key]
			if !ok {
				return nil, controltest.NotFound(req.BlockID)
			}
			if name == "format" {
				return map[string]string{"content": res}, nil
			}
			return json.RawMessage(res), nil
		})
	}
	return f
}

// addSession は session と、その block (block ID と flavor の組) を置く。
func (f *fakeAPI) addSession(session string, blocks ...[2]string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sessions = append(f.sessions, session)
	f.blocks[session] = append([][2]string{}, blocks...)
}

// addUnlisted は block を持つが session.list に出ない session (全列挙の後にできた session) を置く。
func (f *fakeAPI) addUnlisted(session string, blocks ...[2]string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.blocks[session] = append([][2]string{}, blocks...)
}

// listOnly は session.list にだけ出し、list_blocks では無い session (list の後に閉じた session) を置く。
func (f *fakeAPI) listOnly(session string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sessions = append(f.sessions, session)
}

// put は block の method の応答を置く。key は "<method の短い名前>-<block>"。
func (f *fakeAPI) put(key, result string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.results[key] = result
}

func (f *fakeAPI) putBlock(blockID, programStatus, process string) {
	f.put("program_status-"+blockID, programStatus)
	f.put("process-"+blockID, process)
}

func (f *fakeAPI) fail(key string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failing[key] = true
}

func (f *fakeAPI) setStatus(s string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status = s
}

// calls は受けた request を "<method> <block か session>" の形で返す。block の method は短い名前にする。
// session.attach_all は接続ごとの準備なので除く。
func (f *fakeAPI) calls() []string {
	var out []string
	for _, r := range f.srv.Requests() {
		if r.Method == "session.attach_all" {
			continue
		}
		var p struct {
			SessionID string   `json:"session_id"`
			BlockID   string   `json:"block_id"`
			Queries   []string `json:"queries"`
		}
		_ = json.Unmarshal(r.Payload, &p)
		line := strings.TrimPrefix(r.Method, terminalMethod)
		switch {
		case p.BlockID != "":
			line += " " + p.BlockID
		case len(p.Queries) > 0:
			line += " " + strings.Join(p.Queries, ",")
		case p.SessionID != "":
			line += " " + p.SessionID
		}
		out = append(out, line)
	}
	return out
}

// requestsOf は method の request の payload を受けた順に返す。
func (f *fakeAPI) requestsOf(method string) []json.RawMessage {
	var out []json.RawMessage
	for _, r := range f.srv.Requests() {
		if r.Method == method {
			out = append(out, r.Payload)
		}
	}
	return out
}

func (f *fakeAPI) assertCalls(t *testing.T, want ...string) {
	t.Helper()
	got := f.calls()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("API の calls =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// apiClient は fake の API だけに繋ぐ Client。CLI を叩く method は fake の rex が無いので失敗する。
func (f *fakeAPI) client(resolve SessionResolver) *execClient {
	return &execClient{cli: cli.New("/nonexistent/rex", noCommandTimeout), timeout: noCommandTimeout, resolve: resolve, socket: fixedSocket(f.srv.Path)}
}

// clientWith は fake の rex (書き込みと購読) と fake の API (読み取り) の両方に繋ぐ Client。
func (f *fakeAPI) clientWith(rex *fakeRex, resolve SessionResolver) *execClient {
	return &execClient{cli: cli.New(rex.bin, noCommandTimeout), timeout: noCommandTimeout, resolve: resolve, socket: fixedSocket(f.srv.Path)}
}

func fixedSocket(path string) func() (string, error) {
	return func() (string, error) { return path, nil }
}
