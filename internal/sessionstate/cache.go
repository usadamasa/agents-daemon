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

// CacheAckUnknown は sidecar が無い (Stop hook が入っていない､初回ターン) まま送るときに
// ack へ書く sentinel｡guard (TTLGuard) は mtime が直近ならこれを daemon の送信として通し､
// transcript から求めた実際の値で書き換える｡
const CacheAckUnknown = "daemon-unknown"

// cacheFile は cache/<session_id>.json の生の形｡last_request_at は transcript の
// timestamp 文字列を整形せずに写す｡読み手が ack との比較を文字列の完全一致で行うため｡
type cacheFile struct {
	LastRequestAt  string `json:"last_request_at"`
	TTLSeconds     int64  `json:"ttl_seconds"`
	TranscriptPath string `json:"transcript_path"`
}

// Cache はセッション 1 つぶんの prompt cache の状態｡Stop hook のたびに transcript から
// 写される｡LastRequestAt + TTL を過ぎると次のリクエストは文脈全体を cache write として
// 送り直すので､読み手はその前後で送る・送らないを決める｡
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
	// 記録の無い sidecar では 0｡
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

// stopHookInput は Stop hook の stdin のうち要る部分｡
type stopHookInput struct {
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
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
			CacheCreation struct {
				OneHour    float64 `json:"ephemeral_1h_input_tokens"`
				FiveMinute float64 `json:"ephemeral_5m_input_tokens"`
			} `json:"cache_creation"`
		} `json:"usage"`
	} `json:"message"`
}

// IngestStop は Stop hook の stdin (input) から cache/<session_id>.json を書く｡
// `agents-daemon ingest-stop` の本体で､応答が終わるたびに呼ばれる｡
//
//   - session_id が空､または transcript_path が空なら何もしない
//   - transcript が無ければ何もしない (既存の sidecar も残す)
//   - transcript に cache write が 1 つも無ければ (初回ターン､caching 無効) sidecar を消す
//
// 書き込みは同一ディレクトリの一時ファイル経由の rename で原子的に行う｡
func (s Store) IngestStop(input []byte) error {
	var in stopHookInput
	if err := json.Unmarshal(input, &in); err != nil {
		return fmt.Errorf("hook の入力のパースに失敗: %w", err)
	}
	path, err := sessionPath(s.Cache, in.SessionID, ".json")
	if err != nil || path == "" || in.TranscriptPath == "" {
		return err
	}

	tail, err := readTail(in.TranscriptPath, transcriptTailBytes)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("transcript の読み込みに失敗: %w", err)
	}
	lastRequestAt, ttl, ok := lastMainResponse(tail)
	if !ok {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%s の削除に失敗: %w", path, err)
		}
		return nil
	}
	return writeJSONAtomic(path, ".cache.*", cacheFile{
		LastRequestAt:  lastRequestAt,
		TTLSeconds:     int64(ttl / time.Second),
		TranscriptPath: in.TranscriptPath,
	})
}

// lastMainResponse は transcript の末尾から「直近の main 会話の応答の開始時刻」と
// 「最新の cache write の TTL」を返す｡どちらかが見つからなければ ok は false｡
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
func lastMainResponse(data []byte) (lastRequestAt string, ttl time.Duration, ok bool) {
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
	return lastRequestAt, ttl, lastRequestAt != "" && ttl != 0
}

// readTail は path の末尾 n バイトを読む (ファイルがそれより小さければ全部)｡
func readTail(path string, n int64) ([]byte, error) {
	f, err := os.Open(path) // #nosec G304 -- Stop hook が渡す transcript のパスをそのまま開く
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

// LoadCache は sessionID に対応する cache の state を読む｡sessionID が空､または
// ファイルが無ければ (nil, nil)｡JSON や時刻が読めない場合だけエラーを返す｡
func (s Store) LoadCache(sessionID string) (*Cache, error) {
	path, err := sessionPath(s.Cache, sessionID, ".json")
	if err != nil || path == "" {
		return nil, err
	}
	data, err := os.ReadFile(path) // #nosec G304 -- session ID を検証済みのパス
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("cache state の読み込みに失敗: %w", err)
	}
	var raw cacheFile
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("cache state のパースに失敗: %w", err)
	}
	at, err := time.Parse(time.RFC3339Nano, raw.LastRequestAt)
	if err != nil {
		return nil, fmt.Errorf("cache state の last_request_at が時刻として読めない: %w", err)
	}
	return &Cache{
		LastRequestAt:  at,
		LastRequestRaw: raw.LastRequestAt,
		TTL:            time.Duration(raw.TTLSeconds) * time.Second,
		TranscriptPath: raw.TranscriptPath,
	}, nil
}
