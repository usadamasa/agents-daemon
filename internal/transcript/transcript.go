// Package transcript は Claude Code のセッションの transcript (`<configDir>/projects/<project>/<session_id>.jsonl`) の
// 置き場と、末尾の会話の entry の読み取りを持つ。書くのは Claude Code で、daemon は読むだけ。
//
// 読むのは「そのセッションの最後の turn が API error で終わり、まだ次の turn が始まっていないか」を知るためで、
// 読むのは末尾の 1 entry だけ。transcript の形は Claude Code の内部仕様で、版で変わりうる。読む欄は Entry に
// 限り、読めなくなったら Tail が理由を返して呼び手 (daemon、doctor) が黙らずに済むようにする。
//
// cc-premise: 2.1.319 limit-screen API error の entry は type=assistant、isApiErrorMessage=true、error (rate_limit など)、quotaLimits (status、rateLimitType、resetsAt epoch 秒)
package transcript

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/usadamasa/agents-daemon/internal/statefile"
)

// Store は transcript の置き場 (projects ディレクトリ) を持つ。ゼロ値は何も見つけない。
type Store struct {
	Dir string
}

// New は Claude Code の設定ディレクトリ (apppath.ClaudeConfigDir) から置き場を導出する。
func New(claudeConfigDir string) Store {
	return Store{Dir: filepath.Join(claudeConfigDir, "projects")}
}

// Entry は transcript の会話の entry (type が user / assistant) のうち読む欄。
type Entry struct {
	UUID      string
	Type      string
	Timestamp time.Time
	// Version は entry を書いた Claude Code の版。
	Version string
	// Error は API error の種類 (rate_limit、overloaded、server_error など)。API error でなければ空。
	Error          string
	IsAPIError     bool
	APIErrorStatus int
	// Quota は rate_limit の entry に Claude Code が添える上限の中身。無ければ nil (2.1.233 以前と、
	// アカウント全体の rate limit や Fable のモデル単位の上限)。
	Quota *Quota
	// Text は message.content の text を繋いだもの (string の content はそのまま)。
	Text string
}

// Quota は entry の quotaLimits のうち読む欄。
type Quota struct {
	// Status は rejected (弾かれた) など。
	Status string
	// RateLimitType は five_hour / seven_day / seven_day_opus / seven_day_sonnet / seven_day_overage_included / overage。
	RateLimitType string
	// ResetsAt は窓の解除時刻。無ければゼロ値。
	ResetsAt      time.Time
	OverageStatus string
}

// Tail は Store.Tail の結果。Entry が nil なら Reason に読めなかった理由。
type Tail struct {
	Path   string
	Entry  *Entry
	Reason string
}

// locate は sessionID の transcript のパスを返す。無ければ ("", nil)。
// Claude Code は起動時の cwd ごとの project ディレクトリに書き、EnterWorktree で cwd が変わると
// 別の project ディレクトリへ移す (実測)。そのため毎回 projects/*/<sid>.jsonl を引き、複数あれば mtime が新しいものを採る。
// subagent の transcript は <sid>/subagents/ の下で、ここでは見ない。
func (s Store) locate(sessionID string) (string, error) {
	if s.Dir == "" || sessionID == "" {
		return "", nil
	}
	if !statefile.ValidSessionID(sessionID) {
		return "", fmt.Errorf("session ID %q はファイル名に使えない文字を含む", sessionID)
	}
	matches, err := filepath.Glob(filepath.Join(s.Dir, "*", sessionID+".jsonl"))
	if err != nil {
		return "", fmt.Errorf("transcript の走査に失敗: %w", err)
	}
	var best string
	var bestMod time.Time
	for _, m := range matches {
		info, err := os.Stat(m)
		if err != nil || info.IsDir() {
			continue
		}
		if best == "" || info.ModTime().After(bestMod) {
			best, bestMod = m, info.ModTime()
		}
	}
	return best, nil
}

// Tail は sessionID の transcript の末尾の会話の entry を読む。読めなければ Reason に理由を入れる。
func (s Store) Tail(sessionID string) Tail {
	if sessionID == "" {
		return Tail{Reason: "pane に session_id が無い (statusline の記録も Claude Code の登録簿の pid も合わない)"}
	}
	path, err := s.locate(sessionID)
	if err != nil {
		return Tail{Reason: err.Error()}
	}
	if path == "" {
		return Tail{Reason: fmt.Sprintf("transcript が見つからない (%s)", filepath.Join(s.Dir, "*", sessionID+".jsonl"))}
	}
	e, ok, err := ReadTail(path)
	if err != nil {
		return Tail{Path: path, Reason: fmt.Sprintf("transcript の読み取りに失敗: %v", err)}
	}
	if !ok {
		return Tail{Path: path, Reason: fmt.Sprintf("transcript の末尾 %d バイトに会話の entry が無い", maxTailBytes)}
	}
	return Tail{Path: path, Entry: &e}
}

const (
	// tailChunk は末尾から 1 回に読む大きさ。会話の entry の 1 行はふつう数 KB で、tool_result や thinking は数十 KB になる。
	tailChunk = 64 * 1024
	// maxTailBytes は末尾から読む上限。これだけ遡って会話の entry が無ければ諦める。
	maxTailBytes = 8 * 1024 * 1024
)

// rawEntry は 1 行のうち読む欄。
type rawEntry struct {
	Type        string `json:"type"`
	UUID        string `json:"uuid"`
	Timestamp   string `json:"timestamp"`
	Version     string `json:"version"`
	IsMeta      bool   `json:"isMeta"`
	IsSidechain bool   `json:"isSidechain"`
	Error       string `json:"error"`
	IsAPIError  bool   `json:"isApiErrorMessage"`
	APIStatus   int    `json:"apiErrorStatus"`
	Quota       *struct {
		Status        string `json:"status"`
		RateLimitType string `json:"rateLimitType"`
		ResetsAt      int64  `json:"resetsAt"`
		OverageStatus string `json:"overageStatus"`
	} `json:"quotaLimits"`
	Message struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

// ReadTail は path の末尾から最後の会話の entry (type が user / assistant で、isMeta でも subagent の isSidechain でもないもの)
// を読む。無ければ ok=false。末尾に改行の無い行は書きかけとして読まない (Claude Code は非同期に書く)。
//
// isMeta の user の entry (hook の context、system-reminder) は turn の始まりに本体が足すもので、利用者の再開ではない。
// これを会話に数えると、上限で止まった後に hook の挿入だけがあった pane を「再開した」と読んで監視へ戻し、二度と拾えない。
func ReadTail(path string) (Entry, bool, error) {
	f, err := os.Open(path) // #nosec G304 -- 呼び出し元が projects 配下の検証済みのパスを渡す
	if err != nil {
		return Entry{}, false, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return Entry{}, false, err
	}
	size := info.Size()
	var buf []byte
	// trailingDropped は末尾の書きかけの行を捨て終えたか (最初の改行が見つかるまで false)。
	trailingDropped := false
	for offset := size; offset > 0; {
		n := min(int64(tailChunk), offset)
		chunk := make([]byte, n)
		if _, err := f.ReadAt(chunk, offset-n); err != nil && !errors.Is(err, io.EOF) {
			return Entry{}, false, err
		}
		offset -= n
		buf = append(chunk, buf...)
		if size-offset > maxTailBytes {
			return Entry{}, false, nil
		}
		if !trailingDropped {
			// 末尾の改行の後ろに残った書きかけの行は捨てる。
			if i := bytes.LastIndexByte(buf, '\n'); i >= 0 {
				buf = buf[:i+1]
			} else if offset > 0 {
				continue
			} else {
				buf = nil
			}
			trailingDropped = true
		}
		lines := bytes.Split(buf, []byte("\n"))
		// 先頭の行は chunk の境界で切れているかもしれない。ファイルの頭まで読んだときだけ完全な行と見る。
		first := 1
		if offset == 0 {
			first = 0
		}
		for i := len(lines) - 1; i >= first; i-- {
			if e, ok := conversationEntry(lines[i]); ok {
				return e, true, nil
			}
		}
		// 読み終えた行は捨て、切れているかもしれない先頭の行だけ次の chunk に繋ぐ。
		if first == 1 {
			buf = lines[0]
		}
	}
	return Entry{}, false, nil
}

// Parse は transcript の 1 行を読み、会話の entry なら Entry にする。空行、壊れた行、会話でない行は false。
// simulate が fixture の 1 行から Entry を作るのに使う。
func Parse(line []byte) (Entry, bool) {
	return conversationEntry(line)
}

// conversationEntry は 1 行を読み、会話の entry なら Entry にする。空行、壊れた行、会話でない行は false。
func conversationEntry(line []byte) (Entry, bool) {
	if len(bytes.TrimSpace(line)) == 0 {
		return Entry{}, false
	}
	var raw rawEntry
	if json.Unmarshal(line, &raw) != nil {
		return Entry{}, false
	}
	if (raw.Type != "user" && raw.Type != "assistant") || raw.IsMeta || raw.IsSidechain {
		return Entry{}, false
	}
	e := Entry{
		UUID: raw.UUID, Type: raw.Type, Version: raw.Version,
		Error: raw.Error, IsAPIError: raw.IsAPIError, APIErrorStatus: raw.APIStatus,
		Text: contentText(raw.Message.Content),
	}
	if ts, err := time.Parse(time.RFC3339Nano, raw.Timestamp); err == nil {
		e.Timestamp = ts
	}
	if q := raw.Quota; q != nil {
		e.Quota = &Quota{Status: q.Status, RateLimitType: q.RateLimitType, OverageStatus: q.OverageStatus}
		if q.ResetsAt != 0 {
			e.Quota.ResetsAt = time.Unix(q.ResetsAt, 0)
		}
	}
	return e, true
}

// contentText は message.content (string か、{type, text} の配列) の text を繋ぐ。
func contentText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) != nil {
		return ""
	}
	var texts []string
	for _, p := range parts {
		if p.Type == "text" && p.Text != "" {
			texts = append(texts, p.Text)
		}
	}
	return strings.Join(texts, "\n")
}
