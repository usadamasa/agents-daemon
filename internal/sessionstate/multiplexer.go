package sessionstate

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// 端末多重化ツールの種類。context/<session_id>.json の multiplexer.kind に書く値。
const MultiplexerKindRex = "rex"

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

// multiplexerFile は context/<session_id>.json の multiplexer の生の形。
type multiplexerFile struct {
	Kind    string `json:"kind"`
	Session string `json:"session,omitempty"`
	Block   string `json:"block,omitempty"`
}

func newMultiplexerFile(ref MultiplexerRef, ok bool) *multiplexerFile {
	if !ok {
		return nil
	}
	return &multiplexerFile{Kind: ref.Kind, Session: ref.Session, Block: ref.Block}
}

func (m *multiplexerFile) ref() MultiplexerRef {
	return MultiplexerRef{Kind: m.Kind, Session: m.Session, Block: m.Block}
}

// ListMultiplexerBlocks は context/ を 1 回走査し、kind の block の ID から session_id への対応を返す。
// block と session の対応はこの記録 (statusline が書く) だけから引く。
//
// 同じ ID の記録が複数あれば observed_at が最新のもの (同値なら名前順で先) を採る。
// 同じ block で Claude Code を起動し直すと、古い session の記録が prune まで残るため。
// 壊れたファイルは読み飛ばす。ディレクトリが無い・ゼロ値の Store なら空を返す。
func (s Store) ListMultiplexerBlocks(kind string) (map[string]string, error) {
	if s.Context == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(s.Context)
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
		raw, ok := readContextFile(filepath.Join(s.Context, entry.Name()))
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
