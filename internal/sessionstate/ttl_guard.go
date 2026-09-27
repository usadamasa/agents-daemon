package sessionstate

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

// cacheAckFreshness は daemon が ack を書いてから UserPromptSubmit hook が走るまでの猶予｡
// daemon は送信の直前に書くので数秒で足りるが､pane への打ち込みと Claude Code の受理の
// 遅れを見込んで余裕を取る｡
const cacheAckFreshness = 60 * time.Second

// ttlGuardInput は UserPromptSubmit hook の stdin のうち要る部分｡
type ttlGuardInput struct {
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
	Prompt         string `json:"prompt"`
}

// TTLGuard は UserPromptSubmit hook の stdin (input) を見て､prompt cache の失効後の最初の
// prompt を 1 回だけ止める｡止めるときは利用者に見せる警告を返し､通すときは空文字列を返す｡
// `agents-daemon ttl-guard` の本体で､参考実装は claude-token-audit の ttl_guard.py｡
//
//   - スラッシュコマンド､session_id が空､transcript が無い､cache write が無い､warm なら通す
//   - 失効していれば cache-ack/<session_id> を見る｡中身が last_request_at と同じなら通す
//     (同じ idle gap で既に警告したか､daemon が承知で送った)
//   - 中身が違っても mtime が直近なら daemon の送信として通し､実際の値で書き換える｡
//     sentinel (CacheAckUnknown) だけでなく､Stop hook が中断で発火せず sidecar が古いまま
//     daemon が写した値もここで通る
//   - どちらでもなければ書き換えて止める
//
// ack は時間で失効させない (待った時間が長くなっても書き直しのコストは同じ)｡
// 90 日より古い ack はここでも消す｡
func (s Store) TTLGuard(input []byte, now time.Time) (warning string, err error) {
	var in ttlGuardInput
	if err := json.Unmarshal(input, &in); err != nil {
		return "", fmt.Errorf("hook の入力のパースに失敗: %w", err)
	}
	if strings.HasPrefix(strings.TrimSpace(in.Prompt), "/") {
		return "", nil
	}
	ackPath, err := sessionPath(s.CacheAck, in.SessionID, "")
	if err != nil || ackPath == "" || in.TranscriptPath == "" {
		return "", err
	}
	_, pruneErr := pruneDir(s.CacheAck, now, cacheAckRetention)

	tail, err := readTail(in.TranscriptPath, transcriptTailBytes)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", pruneErr
		}
		return "", errors.Join(fmt.Errorf("transcript の読み込みに失敗: %w", err), pruneErr)
	}
	raw, ttl, ok := lastMainResponse(tail)
	if !ok {
		return "", pruneErr
	}
	at, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return "", errors.Join(fmt.Errorf("transcript の timestamp %q が時刻として読めない: %w", raw, err), pruneErr)
	}
	c := Cache{LastRequestAt: at, LastRequestRaw: raw, TTL: ttl}
	if !c.Expired(now) {
		return "", pruneErr
	}

	ack, ackErr := readAck(ackPath)
	if ackErr != nil {
		return "", errors.Join(ackErr, pruneErr)
	}
	if ack.exists && ack.value == raw {
		return "", pruneErr
	}
	if err := writeFileAtomic(ackPath, ".cache-ack.*", []byte(raw)); err != nil {
		return "", errors.Join(fmt.Errorf("ack の書き込みに失敗: %w", err), pruneErr)
	}
	if ack.exists && fresh(ack.modTime, now, cacheAckFreshness) {
		return "", pruneErr
	}
	return ttlWarning(now.Sub(at), ttl), pruneErr
}

type ackState struct {
	exists  bool
	value   string
	modTime time.Time
}

func readAck(path string) (ackState, error) {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ackState{}, nil
		}
		return ackState{}, fmt.Errorf("ack の情報を取得できません: %w", err)
	}
	data, err := os.ReadFile(path) // #nosec G304 -- session ID を検証済みのパス
	if err != nil {
		return ackState{}, fmt.Errorf("ack の読み込みに失敗: %w", err)
	}
	return ackState{exists: true, value: string(data), modTime: info.ModTime()}, nil
}

// ttlWarning は止めたときに利用者へ見せる文面｡/compact を安い再開として勧めない
// (失効後の /compact も全履歴を送り直す)｡
func ttlWarning(elapsed, ttl time.Duration) string {
	var b strings.Builder
	fmt.Fprintf(&b, "前回のやりとりから %d 分経っています｡\n", int(elapsed/time.Minute))
	fmt.Fprintf(&b, "prompt cache は %d 分で失効したため､このまま送ると会話全体を送り直し､費用が大きく増えます｡\n\n",
		int(ttl/time.Minute))
	b.WriteString("  新しいセッションでよい → /clear (最も安い)\n")
	b.WriteString("  すぐ終わる             → 同じ prompt をもう一度送る (この警告は同じ間隔で 1 回だけ)\n")
	b.WriteString("  まだ長く続く           → /compact してから続ける\n")
	b.WriteString("                           (/compact も 1 回は全履歴を送り直す｡以後のターンが要約だけを読むので元が取れる)\n")
	if ttl == 5*time.Minute {
		b.WriteString("\nprompt cache の TTL が 5 分です｡作業の途中で離れることが多いなら､settings.json に\n")
		b.WriteString(`"promptCacheTtl": "1h" (環境変数 CLAUDE_CODE_PROMPT_CACHE_TTL でも可､Claude Code v2.1.242+) を` + "\n")
		b.WriteString("設定すると cache が長く持ちます (cache write の単価は 2 倍)｡\n")
	}
	return b.String()
}
