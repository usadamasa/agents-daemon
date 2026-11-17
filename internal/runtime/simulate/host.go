package simulate

import (
	"context"
	"errors"
	"fmt"

	"github.com/usadamasa/agents-daemon/internal/sessioncontext"
)

// simulateHost は使い捨て pane の一生 (作成・Claude agent に見せる・後始末) を行う。
// multiplexer.Client (pane を見る・読む・送る) に無い
// pane のライフサイクル操作で、simulate だけが使うので Client には足さずこのパッケージに閉じる。
//
// 使い捨て pane の前景は `/bin/sh scriptPath screenPath` (simulateScript)。script が
// limit 風の画面を出したあと `cat -v` として居座り、daemon が送る esc・文字列・enter をそのまま
// 画面に書く。shell を受け手にすると ESC の直後の文字を Meta として食う (zsh の KEYTIMEOUT) など
// shell 側の解釈が混ざり、送信の検証にならない。
type simulateHost interface {
	// SplitPane は自分の pane を分割して使い捨て pane を作り、その前景で script を走らせる。
	SplitPane(ctx context.Context, scriptPath, screenPath string) (string, error)
	// MarkAsClaudeAgent は pane を外から Claude agent に見せ、何をしたかの説明を返す。
	// pane 自身が OSC 7501 で名乗る backend (rex) では script がそれを担い、ここでは何もしない。
	MarkAsClaudeAgent(ctx context.Context, paneID string) (string, error)
	// ProgramStatusCommand は script の先頭で pane 自身に Claude を名乗らせるコマンド。
	ProgramStatusCommand() string
	// ClosePane は SplitPane が作った pane を閉じる。
	ClosePane(ctx context.Context, paneID string) error
	// CloseCommand は手で閉じるときのコマンド (--keep と後始末の失敗時の案内)。
	CloseCommand(paneID string) string
}

// simulateScript は使い捨て pane の前景で走らせる sh script。$1 は画面テキストのパス。
// `cat -v` は受けた入力を解釈せずそのまま書く (ESC は `^[`) ので、送信の内容が画面で読める。
func simulateScript(programStatusCommand string) string {
	s := "#!/bin/sh\n# agents-daemon simulate の使い捨て pane の前景。\n"
	if programStatusCommand != "" {
		s += programStatusCommand + "\n"
	}
	s += "/bin/cat \"$1\"\nexec /bin/cat -v\n"
	return s
}

// paneOps は rex の Client (rex) が持つ、pane を作って閉じる口。simulate だけが使うので multiplexer.Client には無く、
// backend.Connect が返す Client から取り出す。
type paneOps interface {
	// SplitPane は anchor の pane の下に pane を作り、shell を挟まず argv を起こす。終わっても pane を残す。
	SplitPane(ctx context.Context, anchor, label string, argv []string) (string, error)
	// ClosePane は pane を閉じる。
	ClosePane(ctx context.Context, paneID string) error
}

// newSimulateHost は kind の host を作り、使い捨て pane を割り出す基準になる自分の pane が
// 環境から分かることを確かめる。client は backend.Connect が返した Client で、pane を作る口 (paneOps) を持つこと。
func newSimulateHost(kind string, getenv func(string) string, client any) (simulateHost, error) {
	switch kind {
	case sessioncontext.MultiplexerKindRex:
		own := getenv("REX_BLOCK")
		if own == "" {
			return nil, errors.New("REX_BLOCK のある rex の block 内で実行してください (使い捨て block を割り出す anchor になる自分の block が要ります)")
		}
		ops, ok := client.(paneOps)
		if !ok {
			return nil, fmt.Errorf("この backend の Client (%T) は pane を作れない", client)
		}
		return &rexHost{ops: ops, ownBlock: own}, nil
	default:
		return nil, fmt.Errorf("未対応の多重化ツール: %q", kind)
	}
}

// rexHost は rex の Client の SplitPane / ClosePane で使い捨て block を扱う。
// rex には pane を外から Claude agent に登録する手段が無く、Claude の判定は Claude Code 自身が出す
// OSC 7501 (program_status の record) に頼る。そこで使い捨て block の script に
// 同じ OSC 7501 を printf させ、本番と同じ判定 (rex/blockstate の ToPane) を通す。
type rexHost struct {
	ops      paneOps
	ownBlock string
}

// claudeProgramStatusCommand は Claude Code が入力待ちで出すものと同じ書式の OSC 7501。
// cc-premise: 2.1.302 osc7501 payload の書式 (state=…:app=claude-code)
// payload は `key=value` を `:` で繋ぎ、終端は BEL。rex は record の owner.pid と foreground の pid を
// 突き合わせるので、printf は script 自身 (sh の builtin) が出し、その後 exec で同じ pid のまま居座る。
const claudeProgramStatusCommand = `printf '\033]7501;state=idle:app=claude-code\a'`

// SplitPane は自分の block (REX_BLOCK) の下に block を作り、shell を挟まず script を直接走らせる。
// rex の capture は可視領域しか返さないので、limit 風の画面が収まる高さ (40%) を取る (高さは rex が決める)。
// script が落ちたときに画面を読めるよう、終わっても block を残す。
func (h *rexHost) SplitPane(ctx context.Context, scriptPath, screenPath string) (string, error) {
	id, err := h.ops.SplitPane(ctx, h.ownBlock, simulateMetadataSource, []string{"/bin/sh", scriptPath, screenPath})
	if err != nil {
		return "", fmt.Errorf("使い捨て block の作成に失敗: %w", err)
	}
	return id, nil
}

func (h *rexHost) MarkAsClaudeAgent(context.Context, string) (string, error) {
	return "script が OSC 7501 で Claude Code を名乗りました (app=claude-code, state=idle)", nil
}

func (h *rexHost) ProgramStatusCommand() string { return claudeProgramStatusCommand }

func (h *rexHost) ClosePane(ctx context.Context, paneID string) error {
	return h.ops.ClosePane(ctx, paneID)
}

func (h *rexHost) CloseCommand(paneID string) string {
	return "rex block close " + paneID
}
