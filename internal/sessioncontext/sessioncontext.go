// Package sessioncontext は context/<session_id>.json (statusline が観測した context 使用率と、
// Claude Code が動いている端末多重化ツールの場所) の書式を読み書き両方で持つ。
// 書くのは statusline の stdin を受ける sessionstate.IngestStatusline、読むのは
// block から session_id を逆引きする backend (rexcli へ渡す)。
package sessioncontext

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/usadamasa/agents-daemon/internal/statefile"
)

// Retention は context state を残す期間。block から session を逆引きする記録で、
// 5 時間の上限待ちを跨いでも対応が残るよう利用上限 (ratelimit.Retention) より長く取る。
// 「この state がいつまで意味を持つか」は書式の知識なので、消す側
// (sessionstate.Prune) ではなくここに置く。
const Retention = 7 * 24 * time.Hour

// 端末多重化ツールの種類。context/<session_id>.json の multiplexer.kind に書く値。
const MultiplexerKindRex = "rex"

// Store は context/ の置き場所を持つ。ゼロ値の Store は Dir が空文字列で、
// 読み込みは「まだ何も無い」として振る舞う。
type Store struct {
	Dir string
}

// New は XDG state ディレクトリ (apppath.Paths.StateDir) から置き場所を導出する。
func New(stateDir string) Store {
	return Store{Dir: filepath.Join(stateDir, "context")}
}

// MultiplexerRef は Claude Code が動いている端末多重化ツールの場所 (rex の Session と Block)。
type MultiplexerRef struct {
	Kind    string
	Session string
	Block   string
}

// ID は kind の中で場所を指す ID (rex は block)。
func (r MultiplexerRef) ID() string {
	return r.Block
}

// MultiplexerRefFromEnv は statusline の command が Claude Code から継承した環境変数から場所を読む。
// 空の値は無いものとして扱う。
func MultiplexerRefFromEnv(getenv func(string) string) (MultiplexerRef, bool) {
	if block := getenv("REX_BLOCK"); block != "" {
		return MultiplexerRef{Kind: MultiplexerKindRex, Session: getenv("REX_SESSION"), Block: block}, true
	}
	return MultiplexerRef{}, false
}

// Record は context/<session_id>.json に書く 1 回の観測。
type Record struct {
	SessionID      string
	UsedPercentage float64
	ObservedAt     time.Time
	// Multiplexer は端末多重化ツールの外なら nil。
	Multiplexer *MultiplexerRef
}

// contextFile は context/<session_id>.json の生の形。読むのは ListMultiplexerBlocks
// (block から session への逆引き) だけで、observed_at は同じ block の記録が複数あるときに
// 最新を選ぶのに使う。
type contextFile struct {
	SessionID      string  `json:"session_id"`
	UsedPercentage float64 `json:"used_percentage"`
	ObservedAt     int64   `json:"observed_at"`
	// Multiplexer は ListMultiplexerBlocks が block から session を逆引きするのに使う。
	Multiplexer *multiplexerFile `json:"multiplexer,omitempty"`
}

// multiplexerFile は context/<session_id>.json の multiplexer の生の形。
type multiplexerFile struct {
	Kind    string `json:"kind"`
	Session string `json:"session,omitempty"`
	Block   string `json:"block,omitempty"`
}

func newMultiplexerFile(ref *MultiplexerRef) *multiplexerFile {
	if ref == nil {
		return nil
	}
	return &multiplexerFile{Kind: ref.Kind, Session: ref.Session, Block: ref.Block}
}

func (m *multiplexerFile) ref() MultiplexerRef {
	return MultiplexerRef{Kind: m.Kind, Session: m.Session, Block: m.Block}
}

// File は sessionID に対応する context state のパスを返す。
// sessionID が空なら ("", nil)、不正な文字を含む場合はエラーを返す。
func (s Store) File(sessionID string) (string, error) {
	return statefile.SessionPath(s.Dir, sessionID, ".json")
}

// Write は rec を context/<session_id>.json へ書く。
// 書き込みは同一ディレクトリの一時ファイル経由の rename で原子的に行う (statefile)。
func (s Store) Write(rec Record) error {
	path, err := s.File(rec.SessionID)
	if err != nil {
		return err
	}
	if path == "" {
		return errors.New("context の書き込みに session ID が無い")
	}
	return statefile.WriteJSONAtomic(path, ".context.*", contextFile{
		SessionID:      rec.SessionID,
		UsedPercentage: rec.UsedPercentage,
		ObservedAt:     rec.ObservedAt.Unix(),
		Multiplexer:    newMultiplexerFile(rec.Multiplexer),
	})
}

// ListMultiplexerBlocks は context/ を 1 回走査し、kind の block の ID から session_id への対応を返す。
// block と session の対応はこの記録 (statusline が書く) だけから引く。
//
// 同じ ID の記録が複数あれば observed_at が最新のもの (同値なら名前順で先) を採る。
// 同じ block で Claude Code を起動し直すと、古い session の記録が prune まで残るため。
// 壊れたファイルは読み飛ばす。ディレクトリが無い・ゼロ値の Store なら空を返す。
func (s Store) ListMultiplexerBlocks(kind string) (map[string]string, error) {
	if s.Dir == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("context ディレクトリの走査に失敗: %w", err)
	}

	sessions := map[string]string{}
	observed := map[string]int64{}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		raw, ok := readContextFile(filepath.Join(s.Dir, entry.Name()))
		if !ok || raw.SessionID == "" || raw.Multiplexer == nil || raw.Multiplexer.Kind != kind {
			continue
		}
		id := raw.Multiplexer.ref().ID()
		if id == "" {
			continue
		}
		if prev, seen := observed[id]; seen && raw.ObservedAt <= prev {
			continue
		}
		sessions[id] = raw.SessionID
		observed[id] = raw.ObservedAt
	}
	return sessions, nil
}

// ResolveMultiplexerBlock は kind の block の ID から session_id を引く。
// 無ければ ("", nil)。複数の ID を引くなら ListMultiplexerBlocks で 1 回の走査に済ませる。
func (s Store) ResolveMultiplexerBlock(kind, id string) (string, error) {
	sessions, err := s.ListMultiplexerBlocks(kind)
	if err != nil {
		return "", err
	}
	return sessions[id], nil
}

// readContextFile は context file を読む。読めない・壊れているなら false。
func readContextFile(path string) (contextFile, bool) {
	data, err := os.ReadFile(path) // #nosec G304 -- 呼び出し元が context ディレクトリ直下の名前から組み立てる
	if err != nil {
		return contextFile{}, false
	}
	var raw contextFile
	if err := json.Unmarshal(data, &raw); err != nil {
		return contextFile{}, false
	}
	return raw, true
}
