package rexcli

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/usadamasa/agents-daemon/internal/runtime/backend/rexcli/api"
	"github.com/usadamasa/agents-daemon/internal/runtime/backend/rexcli/cli"
	"github.com/usadamasa/agents-daemon/internal/runtime/backend/rexcli/control/controltest"
)

// terminalMethod は block の method の request の method 名。
const terminalMethod = "com.superlogical.terminal."

// fakeAPI は controltest の fake server に、session と block の状態から応答を組む handler を載せたもの。
// 状態に無い session は session_not_found、無い block の method は not_found で答える (実機と同じ)。
type fakeAPI struct {
	srv *controltest.Server
	// rex は client が CLI を起こすときの fake の rex。
	rex *fakeRex

	mu       sync.Mutex
	sessions []string
	// blocks は session ごとの block と flavor。key の無い session は list_blocks が session_not_found になる。
	blocks map[string][][2]string
	// results は "<method の短い名前>-<block>" の応答の JSON。format だけは画面の文字列。
	results map[string]string
	// failing は "<method の短い名前>-<block>" か "list_blocks-<session>" で、internal の失敗を返す。
	failing map[string]bool
	status  string
	// labels は session の label。target.resolve が session を label でも引ける。
	labels map[string]string
}

func newFakeAPI(t *testing.T) *fakeAPI {
	t.Helper()
	f := &fakeAPI{srv: controltest.New(t), rex: newFakeRex(t), blocks: map[string][][2]string{}, results: map[string]string{}, failing: map[string]bool{}, labels: map[string]string{}}
	f.handleWrites()
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
			if len(req.Kinds) == 1 && req.Kinds[0] == "session" {
				for session := range f.blocks {
					if session == q || f.labels[session] == q {
						r = map[string]any{"query": q, "status": "resolved", "target": map[string]any{"kind": "session", "session_id": session, "label": f.labels[session]}}
					}
				}
				results = append(results, r)
				continue
			}
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
	for _, name := range []string{"program_status", "process", "size", "modes", "format", "blob_read"} {
		f.srv.Handle(terminalMethod+name, func(p json.RawMessage) (any, error) {
			var req struct {
				BlockID string `json:"block_id"`
				Args    struct {
					MaxBytes int `json:"max_bytes"`
					Offset   int `json:"offset"`
				} `json:"args"`
			}
			_ = json.Unmarshal(p, &req)
			key := name + "-" + req.BlockID
			if name == "blob_read" {
				key = "format-" + req.BlockID
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.failing[key] {
				return nil, &controltest.Error{Code: "internal", Message: "server error", Status: 500}
			}
			res, ok := f.results[key]
			if !ok {
				return nil, controltest.NotFound(req.BlockID)
			}
			switch name {
			case "format":
				return formatReply(res, req.Args.MaxBytes)
			case "blob_read":
				return blobReply(res, req.Args.Offset, req.Args.MaxBytes), nil
			}
			return json.RawMessage(res), nil
		})
	}
	return f
}

// handleWrites は名指した session と block があれば成功する書き込みの method を載せる。無ければ実機と同じく
// session_not_found か not_found で断る。作る method (session.create、new_window、new_split) の応答は
// テストが srv.Reply で決める。
func (f *fakeAPI) handleWrites() {
	check := func(p json.RawMessage, needBlock bool) error {
		var req struct {
			SessionID string `json:"session_id"`
			BlockID   string `json:"block_id"`
		}
		_ = json.Unmarshal(p, &req)
		f.mu.Lock()
		defer f.mu.Unlock()
		blocks, ok := f.blocks[req.SessionID]
		if !ok {
			return controltest.SessionNotFound(req.SessionID)
		}
		if !needBlock {
			return nil
		}
		for _, b := range blocks {
			if b[0] == req.BlockID {
				return nil
			}
		}
		return controltest.NotFound(req.BlockID)
	}
	for method, needBlock := range map[string]bool{
		terminalMethod + "write": true, "block.event": true, "block.close": true,
		"session.destroy": false, "session.attach": false, "session.disown_block": false,
	} {
		f.srv.Handle(method, func(p json.RawMessage) (any, error) {
			if err := check(p, needBlock); err != nil {
				return nil, err
			}
			return map[string]any{}, nil
		})
	}
}

// addLabel は session に label を付ける。
func (f *fakeAPI) addLabel(session, label string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.labels[session] = label
}

// payloadOf は method の最初の request の payload を読む。
func (f *fakeAPI) payloadOf(t *testing.T, method string, v any) {
	t.Helper()
	reqs := f.requestsOf(method)
	if len(reqs) == 0 {
		t.Fatalf("%s の request が無い", method)
	}
	if err := json.Unmarshal(reqs[0], v); err != nil {
		t.Fatalf("%s の payload を読めない: %v", method, err)
	}
}

// blobID は fake の format が大きな画面に振る id。
const blobID = 7

// formatReply は実機の format と同じく、max_bytes が 0 なら全体を返し、server の上限 (fakeMessageLimit) を
// 超えれば too_large で断る。max_bytes を超える画面は先頭だけを返し、続きを blob_read で読ませる id を付ける。
// fixture は ASCII だけなので、バイト数と JSON の文字列の長さは同じになる。
func formatReply(content string, maxBytes int) (any, error) {
	if maxBytes == 0 {
		if len(content) > fakeMessageLimit {
			return nil, &controltest.Error{Code: "too_large", Message: "control response exceeds 1048576 bytes", Status: 413}
		}
		return map[string]any{"content": content}, nil
	}
	if len(content) <= maxBytes {
		return map[string]any{"content": content, "total": len(content), "eof": true}, nil
	}
	return map[string]any{"content": content[:maxBytes], "id": blobID, "total": len(content), "eof": false}, nil
}

func blobReply(content string, offset, maxBytes int) map[string]any {
	end := min(offset+maxBytes, len(content))
	return map[string]any{"id": blobID, "data": content[offset:end], "total": len(content), "eof": end == len(content)}
}

// fakeMessageLimit は実機の welcome の max_message_bytes。
const fakeMessageLimit = 1 << 20

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

// apiTimeout は fake の API への request の制限時間。本番と同じく呼び出しごとに ctx を切り、
// 切った ctx が接続を巻き込まないことをどのテストも通す。
const apiTimeout = 5 * time.Second

// client は fake の API と、fixture を何も置かない fake の rex に繋ぐ Client。CLI を叩く method は fake の rex が
// exit 3 で断る。無いパスの rex を渡すと Runner が既定の置き場から本物の rex を引き直して叩くので、必ず fake を渡す。
func (f *fakeAPI) client(resolve SessionResolver) *execClient {
	return &execClient{cli: cli.New(f.rex.bin, noCommandTimeout), rex: api.New(fixedSocket(f.srv.Path), apiTimeout), resolve: resolve}
}

// clientWith は fake の rex (書き込みと購読) と fake の API (読み取り) の両方に繋ぐ Client。
func (f *fakeAPI) clientWith(rex *fakeRex, resolve SessionResolver) *execClient {
	return &execClient{cli: cli.New(rex.bin, noCommandTimeout), rex: api.New(fixedSocket(f.srv.Path), apiTimeout), resolve: resolve}
}

func fixedSocket(path string) func() (string, error) {
	return func() (string, error) { return path, nil }
}
