package sessionstate

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// 端末多重化ツールの種類｡context/<session_id>.json の mux.kind に書く値｡
const (
	MuxKindRex   = "rex"
	MuxKindHerdr = "herdr"
)

// MuxRef は Claude Code が動いている端末多重化ツールの場所｡rex は Session と Block､
// herdr は Pane だけを持つ｡
type MuxRef struct {
	Kind    string
	Session string
	Block   string
	Pane    string
}

// ID は kind の中で場所を指す ID (rex は block､herdr は pane)｡
func (r MuxRef) ID() string {
	if r.Kind == MuxKindRex {
		return r.Block
	}
	return r.Pane
}

// MuxRefFromEnv は statusline の command が Claude Code から継承した環境変数から場所を読む｡
// REX_BLOCK を HERDR_PANE_ID より先に見るのは､herdr の中で rex を動かすと両方が見えるため
// (内側が Claude Code の直接の親)｡空の値は無いものとして扱う｡
func MuxRefFromEnv(getenv func(string) string) (MuxRef, bool) {
	if block := getenv("REX_BLOCK"); block != "" {
		return MuxRef{Kind: MuxKindRex, Session: getenv("REX_SESSION"), Block: block}, true
	}
	if pane := getenv("HERDR_PANE_ID"); pane != "" {
		return MuxRef{Kind: MuxKindHerdr, Pane: pane}, true
	}
	return MuxRef{}, false
}

// muxFile は context/<session_id>.json の mux の生の形｡
type muxFile struct {
	Kind    string `json:"kind"`
	Session string `json:"session,omitempty"`
	Block   string `json:"block,omitempty"`
	Pane    string `json:"pane,omitempty"`
}

func newMuxFile(ref MuxRef, ok bool) *muxFile {
	if !ok {
		return nil
	}
	return &muxFile{Kind: ref.Kind, Session: ref.Session, Block: ref.Block, Pane: ref.Pane}
}

func (m *muxFile) ref() MuxRef {
	return MuxRef{Kind: m.Kind, Session: m.Session, Block: m.Block, Pane: m.Pane}
}

// ListMuxBlocks は context/ を 1 回走査し､kind の block (herdr は pane) の ID から
// session_id への対応を返す｡rex には herdr の agent_session に当たるものが無いため､
// statusline が書いた対から逆引きする｡
//
// 同じ ID の記録が複数あれば observed_at が最新のもの (同値なら名前順で先) を採る｡
// 同じ block で Claude Code を起動し直すと､古い session の記録が prune まで残るため｡
// 壊れたファイルは読み飛ばす｡ディレクトリが無い・ゼロ値の Store なら空を返す｡
func (s Store) ListMuxBlocks(kind string) (map[string]string, error) {
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
		if !ok || raw.SessionID == "" || raw.Mux == nil || raw.Mux.Kind != kind {
			continue
		}
		id := raw.Mux.ref().ID()
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

// ResolveMuxBlock は kind の block (herdr は pane) の ID から session_id を引く｡
// 無ければ ("", nil)｡複数の ID を引くなら ListMuxBlocks で 1 回の走査に済ませる｡
func (s Store) ResolveMuxBlock(kind, id string) (string, error) {
	sessions, err := s.ListMuxBlocks(kind)
	if err != nil {
		return "", err
	}
	return sessions[id], nil
}

// readContextFile は context file を読む｡読めない・壊れているなら false｡
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
