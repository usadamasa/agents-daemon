package sessionstate

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"time"
)

// transcriptTailBytes は transcript の末尾から読む量｡直近の応答と最新の cache write が
// 収まれば足りる｡先頭の切れた行は JSON として読めず飛ばされる｡
const transcriptTailBytes = 2 << 20

// CacheAckUnknown は cache の状態が分からない (transcript を辿れない､初回ターン) まま送るときに
// ack へ書く sentinel｡guard (TTLGuard) は mtime が直近ならこれを daemon の送信として通し､
// transcript から求めた実際の値で書き換える｡
const CacheAckUnknown = "daemon-unknown"

// Cache はセッション 1 つぶんの prompt cache の状態｡daemon が transcript から求める｡
// LastRequestAt + TTL を過ぎると次のリクエストは文脈全体を cache write として送り直すので､
// 読み手はその前後で送る・送らないを決める｡
type Cache struct {
	// LastRequestAt は直近の main 会話の応答が始まった時刻 (= cache が最後に更新された時刻)｡
	LastRequestAt time.Time
	// LastRequestRaw は LastRequestAt の transcript 上の文字列｡ack との完全一致に使う｡
	LastRequestRaw string
	// TTL は最新の cache write に使われた TTL (5 分か 1 時間)｡
	TTL time.Duration
	// TranscriptPath はその transcript のパス｡
	TranscriptPath string
	// ContextTokens は直近の main 会話の応答の入力トークン数 (input + cache_creation + cache_read)｡
	ContextTokens int64
}

// ExpiresAt は cache が失効する時刻 (LastRequestAt + TTL)｡
func (c *Cache) ExpiresAt() time.Time {
	return c.LastRequestAt.Add(c.TTL)
}

// Expired は now 時点で cache が失効しているか (now - LastRequestAt >= TTL) を返す｡
// LastRequestAt が未来 (clock skew) なら warm 扱い｡
func (c *Cache) Expired(now time.Time) bool {
	return !now.Before(c.ExpiresAt())
}

// WriteCacheAck は cache-ack/<session_id> に value を書く｡daemon が非スラッシュの prompt を
// 送る直前に呼び､TTL guard hook がこれを見て daemon の送信を止めずに通す｡
//
// value は Cache.LastRequestRaw (transcript の timestamp 文字列そのまま) か CacheAckUnknown｡
// guard との比較は文字列の完全一致なので､整形も末尾の改行も入れない｡同じ値でも書き直す
// (guard は mtime が直近かで daemon の送信かを見る)｡sessionID が空なら何もしない｡
func (s Store) WriteCacheAck(sessionID, value string) error {
	path, err := sessionPath(s.CacheAck, sessionID, "")
	if err != nil || path == "" {
		return err
	}
	return writeFileAtomic(path, ".cache-ack.*", []byte(value))
}

// transcriptEntry は transcript JSONL の 1 行のうち要る部分｡形式は Claude Code の内部
// 形式で version 間で変わりうるため､読めない行は飛ばす (fail-open)｡
type transcriptEntry struct {
	Type        string `json:"type"`
	Subtype     string `json:"subtype"`
	IsSidechain bool   `json:"isSidechain"`
	Timestamp   string `json:"timestamp"`
	Message     struct {
		ID    string `json:"id"`
		Model string `json:"model"`
		Usage struct {
			InputTokens              float64 `json:"input_tokens"`
			CacheCreationInputTokens float64 `json:"cache_creation_input_tokens"`
			CacheReadInputTokens     float64 `json:"cache_read_input_tokens"`
			CacheCreation            struct {
				OneHour    float64 `json:"ephemeral_1h_input_tokens"`
				FiveMinute float64 `json:"ephemeral_5m_input_tokens"`
			} `json:"cache_creation"`
		} `json:"usage"`
	} `json:"message"`
}

// lastMainResponse は transcript の末尾から「直近の main 会話の応答の開始時刻」と
// 「最新の cache write の TTL」､「直近の応答の入力トークン数」を返す｡開始時刻か TTL が
// 見つからなければ ok は false｡
//
// 入力トークン数は statusline の used_percentage と同じ式 (input + cache_creation +
// cache_read､output は含めない) で数える｡
//
// 規則は tatsuo48/claude-token-audit の ttl_guard.py (last_main_response) と同じ:
//
//   - type が assistant で isSidechain でない行だけ見る｡model が "<synthetic>" の行は
//     API リクエストが無いので飛ばす
//   - 1 応答は content block ごとに複数行に分かれ message.id が同じ｡cache が更新される
//     のはリクエスト開始時なので､同じ id の最も早い timestamp を採る
//   - TTL は最新の行ではなく最新の write (ephemeral_*_input_tokens > 0) から採る｡
//     最新の応答が pure hit でも､その前の write の TTL が生きている
//
// compact_boundary より前は見ない (参考実装には無い規則)｡compact 後の次のリクエストは
// 要約だけを送るので､compact 前の cache の状態は意味を持たない｡
func lastMainResponse(data []byte) (lastRequestAt string, ttl time.Duration, tokens int64, ok bool) {
	var lastID string
	idKnown := false
	for _, line := range slices.Backward(bytes.Split(data, []byte{'\n'})) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var e transcriptEntry
		if err := json.Unmarshal(line, &e); err != nil {
			continue
		}
		if e.Type == "system" && e.Subtype == "compact_boundary" && !e.IsSidechain {
			break
		}
		if e.Type != "assistant" || e.IsSidechain || e.Message.Model == "<synthetic>" {
			continue
		}
		if !idKnown {
			lastID, idKnown = e.Message.ID, true
			// 同じ id の行は usage も同じなので､最初に見た行から採る｡
			u := e.Message.Usage
			tokens = int64(u.InputTokens + u.CacheCreationInputTokens + u.CacheReadInputTokens)
		}
		if e.Message.ID == lastID && e.Timestamp != "" {
			lastRequestAt = e.Timestamp
		} else if ttl != 0 {
			break
		}
		if ttl == 0 {
			cc := e.Message.Usage.CacheCreation
			switch {
			case cc.OneHour > 0:
				ttl = time.Hour
			case cc.FiveMinute > 0:
				ttl = 5 * time.Minute
			}
		}
	}
	return lastRequestAt, ttl, tokens, lastRequestAt != "" && ttl != 0
}

// CacheLoader は statusline が context/ に記録した transcript_path を辿り､transcript から
// cache の状態を求める｡Stop hook では読まない｡transcript は非同期に書かれ､Stop の時点では
// ターンの最後の応答を含まないことがあるため (Claude Code の hooks のドキュメント)｡
//
// transcript の大きさと mtime が前回と同じなら読み直さない｡並行には使わない (daemon の tick
// だけが呼ぶ)｡
type CacheLoader struct {
	store Store
	memo  map[string]cacheMemo
}

// cacheMemo は session 1 つぶんの前回の結果｡
type cacheMemo struct {
	path    string
	size    int64
	modTime time.Time
	cache   *Cache
}

// NewCacheLoader は s の context/ を起点に transcript を辿る CacheLoader を返す｡
func NewCacheLoader(s Store) *CacheLoader {
	return &CacheLoader{store: s, memo: map[string]cacheMemo{}}
}

// Load は sessionID の cache の状態を返す｡cache の状態が無い (session が空､context や
// transcript_path や transcript が無い､cache write が無い､compact 後にまだ応答が無い) なら
// (nil, nil)｡context が壊れているか transcript が読めないときだけエラーを返す｡
//
// 読めない transcript のエラーは､transcript が変わるまで 1 回だけ返す (tick ごとにログへ
// 同じ行を書かない)｡
func (l *CacheLoader) Load(sessionID string) (*Cache, error) {
	contextPath, err := sessionPath(l.store.Context, sessionID, ".json")
	if err != nil || contextPath == "" {
		return nil, err
	}
	path, err := readTranscriptPath(contextPath)
	if err != nil || path == "" {
		delete(l.memo, sessionID)
		return nil, err
	}
	info, err := os.Stat(path)
	if err != nil {
		delete(l.memo, sessionID)
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("transcript の情報を取得できません: %w", err)
	}
	if m, ok := l.memo[sessionID]; ok && m.path == path && m.size == info.Size() && m.modTime.Equal(info.ModTime()) {
		return m.cache, nil
	}
	c, err := readCache(path)
	l.memo[sessionID] = cacheMemo{path: path, size: info.Size(), modTime: info.ModTime(), cache: c}
	return c, err
}

// Cached は前回 Load した値を返す｡transcript は読まない｡Load したことが無ければ nil｡
func (l *CacheLoader) Cached(sessionID string) *Cache {
	return l.memo[sessionID].cache
}

// Retain は keep に無い session の前回の結果を捨てる｡
func (l *CacheLoader) Retain(keep map[string]bool) {
	for sessionID := range l.memo {
		if !keep[sessionID] {
			delete(l.memo, sessionID)
		}
	}
}

// readTranscriptPath は context/<session_id>.json の transcript_path を返す｡ファイルが
// 無ければ空｡
func readTranscriptPath(contextPath string) (string, error) {
	data, err := os.ReadFile(contextPath) // #nosec G304 -- session ID を検証済みのパス
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", fmt.Errorf("context state の読み込みに失敗: %w", err)
	}
	var raw contextFile
	if err := json.Unmarshal(data, &raw); err != nil {
		return "", fmt.Errorf("context state のパースに失敗: %w", err)
	}
	return raw.TranscriptPath, nil
}

// readCache は transcript の末尾から cache の状態を求める｡cache の状態が無ければ nil｡
func readCache(path string) (*Cache, error) {
	tail, err := readTail(path, transcriptTailBytes)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("transcript の読み込みに失敗: %w", err)
	}
	raw, ttl, tokens, ok := lastMainResponse(tail)
	if !ok {
		return nil, nil
	}
	at, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return nil, fmt.Errorf("transcript の timestamp %q が時刻として読めない: %w", raw, err)
	}
	return &Cache{LastRequestAt: at, LastRequestRaw: raw, TTL: ttl, TranscriptPath: path, ContextTokens: tokens}, nil
}

// readTail は path の末尾 n バイトを読む (ファイルがそれより小さければ全部)｡
func readTail(path string, n int64) ([]byte, error) {
	f, err := os.Open(path) // #nosec G304 -- Claude Code が渡した transcript のパスをそのまま開く
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if size := info.Size(); size > n {
		if _, err := f.Seek(size-n, io.SeekStart); err != nil {
			return nil, err
		}
	}
	return io.ReadAll(f)
}
