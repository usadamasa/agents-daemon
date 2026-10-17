package simulate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"time"

	"github.com/usadamasa/agents-daemon/internal/sessionstate"
)

// simulateHost は使い捨て pane の一生 (作成・Claude agent に見せる・後始末) を行う。
// multiplexer.Client (PaneList/PaneRead/SendText/SendKeys/Reachable) に無い
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

// rawCommandTimeout は backend の CLI を直接叩く 1 回あたりの上限。
const rawCommandTimeout = 10 * time.Second

// newSimulateHost は kind の host を作り、使い捨て pane を割り出す基準になる自分の pane が
// 環境から分かることを確かめる。
func newSimulateHost(kind string, getenv func(string) string) (simulateHost, error) {
	switch kind {
	case sessionstate.MultiplexerKindRex:
		own := getenv("REX_BLOCK")
		if own == "" {
			return nil, errors.New("REX_BLOCK のある rex の block 内で実行してください (使い捨て block を割り出す anchor になる自分の block が要ります)")
		}
		return &rexHost{bin: binPath(getenv, "REX_BIN_PATH", "rex"), ownBlock: own}, nil
	default:
		return nil, fmt.Errorf("未対応の多重化ツール: %q", kind)
	}
}

func binPath(getenv func(string) string, key, fallback string) string {
	if v := getenv(key); v != "" {
		return v
	}
	return fallback
}

// runRaw は backend の CLI を直接叩き stdout を返す。失敗は stderr を添えた error にする。
func runRaw(ctx context.Context, bin string, args ...string) ([]byte, error) {
	runCtx, cancel := context.WithTimeout(ctx, rawCommandTimeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, bin, args...) // #nosec G204,G702 -- bin は固定既定値/環境変数、args はこのファイル内で組み立てた引数のみ
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return outBuf.Bytes(), fmt.Errorf("%s %v: %w (stderr=%q)", bin, args, err, errBuf.Bytes())
	}
	return outBuf.Bytes(), nil
}

// rexHost は rex の split / block close で使い捨て block を扱う。
// rex には pane を外から Claude agent に登録する手段が無く、Claude の判定は Claude Code 自身が出す
// OSC 7501 (`block call program_status` の record) に頼る。そこで使い捨て block の script に
// 同じ OSC 7501 を printf させ、本番と同じ判定 (rexcli の toPane) を通す。
type rexHost struct {
	bin      string
	ownBlock string
}

// claudeProgramStatusCommand は Claude Code が入力待ちで出すものと同じ書式の OSC 7501。
// cc-premise: 2.1.302 osc7501 payload の書式 (state=…:app=claude-code)
// payload は `key=value` を `:` で繋ぎ、終端は BEL。rexcli は record の owner.pid と foreground の pid を
// 突き合わせるので、printf は script 自身 (sh の builtin) が出し、その後 exec で同じ pid のまま居座る。
const claudeProgramStatusCommand = `printf '\033]7501;state=idle:app=claude-code\a'`

// run は rex を実行する。server が落ちているときに CLI が起こさないよう autostart を切る (rexcli と同じ)。
func (h *rexHost) run(ctx context.Context, args ...string) ([]byte, error) {
	return runRaw(ctx, h.bin, append([]string{"--autostart=false"}, args...)...)
}

// SplitPane は自分の block (REX_BLOCK) の下に block を作り、shell を挟まず script を直接走らせる
// (--shell=none)。rex の capture は可視領域しか返さないので、limit 風の画面が収まる高さ (40%) を取る。
// script が落ちたときに画面を読めるよう --keep-open を付ける。
func (h *rexHost) SplitPane(ctx context.Context, scriptPath, screenPath string) (string, error) {
	out, err := h.run(ctx, "split", "-b", h.ownBlock, "--split=below", "--ratio", "40", "--focus=false", "--keep-open", "--shell=none", "--label", simulateMetadataSource, "--json", "--", "/bin/sh", scriptPath, screenPath)
	if err != nil {
		return "", fmt.Errorf("使い捨て block の作成に失敗: %w", err)
	}
	var res struct {
		BlockIDs []string `json:"block_ids"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(out), &res); err != nil {
		return "", fmt.Errorf("split の応答を解釈できません: %w (stdout=%q)", err, out)
	}
	if len(res.BlockIDs) == 0 || res.BlockIDs[0] == "" {
		return "", fmt.Errorf("split の応答に block_ids が含まれていません (stdout=%q)", out)
	}
	return res.BlockIDs[0], nil
}

func (h *rexHost) MarkAsClaudeAgent(context.Context, string) (string, error) {
	return "script が OSC 7501 で Claude Code を名乗りました (app=claude-code, state=idle)", nil
}

func (h *rexHost) ProgramStatusCommand() string { return claudeProgramStatusCommand }

func (h *rexHost) ClosePane(ctx context.Context, paneID string) error {
	_, err := h.run(ctx, "block", "close", paneID)
	return err
}

func (h *rexHost) CloseCommand(paneID string) string {
	return "rex block close " + paneID
}
