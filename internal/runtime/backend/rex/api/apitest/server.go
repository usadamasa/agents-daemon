// Package apitest は rex の状態 (session、block、block の method の応答) から control API の応答を組む fake の server を
// 提供する。rex の adapter のテストが使う。プロトコル (hello、attach の要求) は controltest が真似る。
// 状態に無い session は session_not_found、無い block の method は not_found で答える (実機と同じ)。
package apitest

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/usadamasa/agents-daemon/internal/runtime/backend/rex/api/control/controltest"
)

// TerminalMethod は terminal の block の method の接頭辞。
const TerminalMethod = "com.superlogical.terminal."

// BlobID は大きな画面の format が続きを読ませるのに振る id。
const BlobID = 7

// messageLimit は実機の welcome の max_message_bytes。
const messageLimit = 1 << 20

// Server は fake の rex server。
type Server struct {
	srv *controltest.Server

	mu       sync.Mutex
	sessions []string
	// blocks は session ごとの block (block ID と flavor の組)。key の無い session は list_blocks が session_not_found になる。
	blocks map[string][][2]string
	// results は method の短い名前と block から、応答の JSON (format だけは画面の文字列)。
	results map[string]string
	// failing は method の短い名前と block か session で、internal の失敗を返すもの。
	failing map[string]bool
	status  string
	// version は server.version の応答。空なら失敗を返す。既定は revision 14 の fake の版。
	version string
	// labels は session の label。
	labels map[string]string
	// closing は閉じかけの session。list_blocks が conflict になる。
	closing map[string]bool
	// truncated は blob_read が続きを返さない block。
	truncated map[string]bool
}

// New は fake の rex server を起こす。t の終わりに閉じる。
func New(t testing.TB) *Server {
	t.Helper()
	s := &Server{
		srv: controltest.New(t), blocks: map[string][][2]string{}, results: map[string]string{}, failing: map[string]bool{},
		labels: map[string]string{}, closing: map[string]bool{}, truncated: map[string]bool{},
		// 購読の同期も server.version を打つので、既定で応える。
		version: `{"rex_version":"0.0.0-fake","built_at":"2026-01-01T00:00:00Z","dirty":false,"api_revision":14}`,
	}
	s.srv.Handle("server.status", s.serverStatus)
	s.srv.Handle("server.version", s.serverVersion)
	// describe_block は block の method と同じく、Put で置いた応答を block ごとに返す。
	s.srv.Handle("session.describe_block", func(p json.RawMessage) (any, error) { return s.blockMethod("describe_block", p) })
	s.srv.Handle("session.list", s.listSessions)
	s.srv.Handle("session.list_blocks", s.listBlocks)
	s.srv.Handle("target.resolve", s.resolve)
	for _, name := range []string{"program_status", "process", "size", "modes", "format", "blob_read"} {
		s.srv.Handle(TerminalMethod+name, func(p json.RawMessage) (any, error) { return s.blockMethod(name, p) })
	}
	// 名指した session と block があれば成功する書き込み。作る method (session.create、new_window、new_split) の応答は
	// テストが Protocol().Reply で決める。
	for method, needBlock := range map[string]bool{
		TerminalMethod + "write": true, "block.event": true, "block.close": true,
		"session.destroy": false, "session.attach": false, "session.disown_block": false,
	} {
		s.srv.Handle(method, func(p json.RawMessage) (any, error) {
			if err := s.exists(p, needBlock); err != nil {
				return nil, err
			}
			return map[string]any{}, nil
		})
	}
	// session.detach は実機と同じく、attach していなくても、session が無くても成功する。
	s.srv.Handle("session.detach", func(json.RawMessage) (any, error) { return map[string]any{}, nil })
	return s
}

// exists は request が名指す session (needBlock なら block も) が状態にあるかを見る。無ければ実機と同じ失敗を返す。
func (s *Server) exists(p json.RawMessage, needBlock bool) error {
	var req struct {
		SessionID string `json:"session_id"`
		BlockID   string `json:"block_id"`
	}
	_ = json.Unmarshal(p, &req)
	s.mu.Lock()
	defer s.mu.Unlock()
	blocks, ok := s.blocks[req.SessionID]
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

// AddLabel は session に label を付ける。target.resolve が session を label でも引ける。
func (s *Server) AddLabel(session, label string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.labels[session] = label
}

// Socket は待ち受けている socket のパス。
func (s *Server) Socket() string { return s.srv.Path }

// Protocol は下の controltest の server。method の応答を差し替える、接続を切る、hello を数えるのに使う。
func (s *Server) Protocol() *controltest.Server { return s.srv }

func key(method, target string) string { return method + " " + target }

// AddSession は session と、その block (block ID と flavor の組) を置く。
func (s *Server) AddSession(session string, blocks ...[2]string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions = append(s.sessions, session)
	s.blocks[session] = append([][2]string{}, blocks...)
}

// SetBlocks は置いた session の block を差し替える。
func (s *Server) SetBlocks(session string, blocks ...[2]string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.blocks[session] = append([][2]string{}, blocks...)
}

// AddUnlisted は block を持つが session.list に出ない session (全列挙の後にできた session) を置く。
func (s *Server) AddUnlisted(session string, blocks ...[2]string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.blocks[session] = append([][2]string{}, blocks...)
}

// ListOnly は session.list にだけ出し、list_blocks では無い session (list の後に閉じた session) を置く。
func (s *Server) ListOnly(session string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions = append(s.sessions, session)
}

// Put は block の method (短い名前と describe_block) の応答を置く。format では画面の文字列を置く。
func (s *Server) Put(method, blockID, result string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.results[key(method, blockID)] = result
}

// PutBlock は block の program_status と process の応答を置く。
func (s *Server) PutBlock(blockID, programStatus, process string) {
	s.Put("program_status", blockID, programStatus)
	s.Put("process", blockID, process)
}

// Fail は method (block の method は短い名前) に、target (block か session) を名指す request で internal の失敗を返させる。
func (s *Server) Fail(method, target string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failing[key(method, target)] = true
}

// Closing は session を閉じかけにする。list_blocks は実機の閉じかけの session と同じく conflict を返す。
func (s *Server) Closing(session string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closing[session] = true
}

// TruncateBlob は block の blob_read に、続きがあるのに空の data を返させる。
func (s *Server) TruncateBlob(blockID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.truncated[blockID] = true
}

// SetStatus は server.status の応答を置く。空なら失敗を返す。
func (s *Server) SetStatus(status string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status = status
}

// SetVersion は server.version の応答を置く。空なら失敗を返す。
func (s *Server) SetVersion(version string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.version = version
}

// Calls は受けた request を "<method> <block か query か session>" の形で受けた順に返す。block の method は短い名前にする。
// session.attach_all は接続ごとの準備なので除く。
func (s *Server) Calls() []string {
	var out []string
	for _, r := range s.srv.Requests() {
		if r.Method == "session.attach_all" {
			continue
		}
		var p struct {
			SessionID string   `json:"session_id"`
			BlockID   string   `json:"block_id"`
			Queries   []string `json:"queries"`
		}
		_ = json.Unmarshal(r.Payload, &p)
		line := strings.TrimPrefix(r.Method, TerminalMethod)
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

// RequestsOf は method の request の payload を受けた順に返す。
func (s *Server) RequestsOf(method string) []json.RawMessage {
	var out []json.RawMessage
	for _, r := range s.srv.Requests() {
		if r.Method == method {
			out = append(out, r.Payload)
		}
	}
	return out
}

func internalError() error {
	return &controltest.Error{Code: "internal", Message: "server error", Status: 500}
}

func (s *Server) serverStatus(json.RawMessage) (any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.status == "" {
		return nil, internalError()
	}
	return json.RawMessage(s.status), nil
}

func (s *Server) serverVersion(json.RawMessage) (any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.version == "" {
		return nil, internalError()
	}
	return json.RawMessage(s.version), nil
}

func (s *Server) listSessions(json.RawMessage) (any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sessions := make([]map[string]string, 0, len(s.sessions))
	for _, id := range s.sessions {
		sessions = append(sessions, map[string]string{"session_id": id, "label": "shy-alcove"})
	}
	return map[string]any{"sessions": sessions}, nil
}

func (s *Server) listBlocks(p json.RawMessage) (any, error) {
	var req struct {
		SessionID string `json:"session_id"`
	}
	_ = json.Unmarshal(p, &req)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failing[key("session.list_blocks", req.SessionID)] {
		return nil, internalError()
	}
	if s.closing[req.SessionID] {
		return nil, &controltest.Error{Code: "conflict", Message: "session " + req.SessionID + " is shutting down", Status: 409}
	}
	blocks, ok := s.blocks[req.SessionID]
	if !ok {
		return nil, controltest.SessionNotFound(req.SessionID)
	}
	out := make([]map[string]string, 0, len(blocks))
	for _, b := range blocks {
		out = append(out, map[string]string{"block_id": b[0], "label": "shell", "creator_name": "com.superlogical.terminal", "flavor": b[1], "placement": "placed"})
	}
	return map[string]any{"blocks": out}, nil
}

func (s *Server) resolve(p json.RawMessage) (any, error) {
	var req struct {
		Kinds   []string `json:"kinds"`
		Queries []string `json:"queries"`
	}
	_ = json.Unmarshal(p, &req)
	s.mu.Lock()
	defer s.mu.Unlock()
	results := make([]map[string]any, 0, len(req.Queries))
	for _, q := range req.Queries {
		r := map[string]any{"query": q, "status": "not_found"}
		if len(req.Kinds) == 1 && req.Kinds[0] == "session" {
			results = append(results, s.resolveSession(q))
			continue
		}
		for session, blocks := range s.blocks {
			for _, b := range blocks {
				if b[0] == q {
					r = map[string]any{"query": q, "status": "resolved", "target": map[string]any{"kind": "block", "session_id": session, "block_id": q, "label": "shell"}}
				}
			}
		}
		results = append(results, r)
	}
	return map[string]any{"results": results}, nil
}

// resolveSession は実機と同じく、完全な ID を先に、無ければ label を大小文字を区別せずに引く。label が 2 つ以上に
// 当たれば ambiguous。s.mu を持って呼ぶ。
func (s *Server) resolveSession(q string) map[string]any {
	if _, ok := s.blocks[q]; ok {
		return map[string]any{"query": q, "status": "resolved", "target": map[string]any{"kind": "session", "session_id": q, "label": s.labels[q]}}
	}
	var matched []string
	for session, label := range s.labels {
		if _, ok := s.blocks[session]; ok && strings.EqualFold(label, q) {
			matched = append(matched, session)
		}
	}
	switch len(matched) {
	case 0:
		return map[string]any{"query": q, "status": "not_found"}
	case 1:
		return map[string]any{"query": q, "status": "resolved", "target": map[string]any{"kind": "session", "session_id": matched[0], "label": s.labels[matched[0]]}}
	}
	candidates := make([]map[string]any, 0, len(matched))
	for _, session := range matched {
		candidates = append(candidates, map[string]any{"kind": "session", "session_id": session, "label": s.labels[session]})
	}
	return map[string]any{"query": q, "status": "ambiguous", "candidates": candidates}
}

func (s *Server) blockMethod(name string, p json.RawMessage) (any, error) {
	var req struct {
		BlockID string `json:"block_id"`
		Args    struct {
			MaxBytes int `json:"max_bytes"`
			Offset   int `json:"offset"`
		} `json:"args"`
	}
	_ = json.Unmarshal(p, &req)
	stored := name
	if name == "blob_read" {
		stored = "format"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failing[key(stored, req.BlockID)] {
		return nil, internalError()
	}
	res, ok := s.results[key(stored, req.BlockID)]
	if !ok {
		return nil, controltest.NotFound(req.BlockID)
	}
	switch name {
	case "format":
		return formatReply(res, req.Args.MaxBytes)
	case "blob_read":
		return blobReply(res, req.Args.Offset, req.Args.MaxBytes, s.truncated[req.BlockID]), nil
	}
	return json.RawMessage(res), nil
}

// formatReply は実機の format と同じく、max_bytes が 0 なら全体を返し、server の上限を超えれば too_large で断る。
// max_bytes を超える画面は先頭だけを返し、続きを blob_read で読ませる id を付ける。
// fixture は ASCII だけなので、バイト数と JSON の文字列の長さは同じになる。
func formatReply(content string, maxBytes int) (any, error) {
	if maxBytes == 0 {
		if len(content) > messageLimit {
			return nil, &controltest.Error{Code: "too_large", Message: "control response exceeds 1048576 bytes", Status: 413}
		}
		return map[string]any{"content": content}, nil
	}
	if len(content) <= maxBytes {
		return map[string]any{"content": content, "total": len(content), "eof": true}, nil
	}
	return map[string]any{"content": content[:maxBytes], "id": BlobID, "total": len(content), "eof": false}, nil
}

// blobReply は実機と同じく、続きが残っていても max_bytes より短く返しうる (実機は文字の途中で切らず、JSON に
// encode した長さで量る)。fake は毎回 max_bytes の半分だけ返し、呼び手が受けた分だけ offset を進めるかを試す。
// truncated なら続きがあるのに空の data を返す。
func blobReply(content string, offset, maxBytes int, truncated bool) map[string]any {
	if truncated {
		return map[string]any{"id": BlobID, "data": "", "total": len(content), "eof": false}
	}
	end := min(offset+maxBytes/2, len(content))
	return map[string]any{"id": BlobID, "data": content[offset:end], "total": len(content), "eof": end == len(content)}
}
