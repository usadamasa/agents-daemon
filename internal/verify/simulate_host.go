package verify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/usadamasa/agents-daemon/internal/sessionstate"
)

// simulateHost は使い捨て pane の一生 (作成・Claude agent に見せる・画面の書き込み・後始末) を
// backend ごとに行う｡multiplexer.Client (PaneList/PaneRead/SendText/SendKeys/ReportMetadata/
// Reachable) に無い pane のライフサイクル操作で､simulate だけが使うので Client には足さず
// このパッケージに閉じる｡
type simulateHost interface {
	// SplitPane は自分の pane を分割して使い捨て pane を作り､その ID を返す｡
	SplitPane(ctx context.Context) (string, error)
	// MarkAsClaudeAgent は pane を Claude agent に見せ､何をしたかの説明を返す｡
	MarkAsClaudeAgent(ctx context.Context, paneID string) (string, error)
	// WriteScreen は pane のシェルに path の内容を表示させる｡画面への反映は待たない｡
	WriteScreen(ctx context.Context, paneID, path string) error
	// ClosePane は SplitPane が作った pane を閉じる｡
	ClosePane(ctx context.Context, paneID string) error
	// CloseCommand は手で閉じるときのコマンド (--keep と後始末の失敗時の案内)｡
	CloseCommand(paneID string) string
}

// rawCommandTimeout は backend の CLI を直接叩く 1 回あたりの上限｡
const rawCommandTimeout = 10 * time.Second

// defaultKeyDelay はコマンド文字列を入力してから Enter を送るまでの間隔｡続けて送ると
// 文字列の消化より先に Enter が届くことがある (herdr で実測)｡
const defaultKeyDelay = 300 * time.Millisecond

// newSimulateHost は kind の host を作り､使い捨て pane を割り出す基準になる自分の pane が
// 環境から分かることを確かめる｡
func newSimulateHost(kind string, getenv func(string) string) (simulateHost, error) {
	switch kind {
	case sessionstate.MultiplexerKindHerdr:
		if getenv("HERDR_ENV") != "1" {
			return nil, errors.New("HERDR_ENV=1 の herdr セッション内で実行してください (使い捨て pane を割り出す基準になる自分の pane が要ります)")
		}
		return &herdrHost{bin: binPath(getenv, "HERDR_BIN_PATH", "herdr"), keyDelay: defaultKeyDelay}, nil
	case sessionstate.MultiplexerKindRex:
		own := getenv("REX_BLOCK")
		if own == "" {
			return nil, errors.New("REX_BLOCK のある rex の block 内で実行してください (使い捨て block を割り出す anchor になる自分の block が要ります)")
		}
		return &rexHost{bin: binPath(getenv, "REX_BIN_PATH", "rex"), ownBlock: own, keyDelay: defaultKeyDelay}, nil
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

// runRaw は backend の CLI を直接叩き stdout を返す｡失敗は stderr を添えた error にする｡
func runRaw(ctx context.Context, bin string, args ...string) ([]byte, error) {
	runCtx, cancel := context.WithTimeout(ctx, rawCommandTimeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, bin, args...) // #nosec G204,G702 -- bin は固定既定値/環境変数､args はこのファイル内で組み立てた引数のみ
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return outBuf.Bytes(), fmt.Errorf("%s %v: %w (stderr=%q)", bin, args, err, errBuf.Bytes())
	}
	return outBuf.Bytes(), nil
}

// herdrHost は herdr の pane split / report-agent / run / send-keys / release-agent / close で
// 使い捨て pane を扱う｡
type herdrHost struct {
	bin      string
	keyDelay time.Duration
}

func (h *herdrHost) run(ctx context.Context, args ...string) ([]byte, error) {
	return runRaw(ctx, h.bin, args...)
}

// SplitPane は現在の pane (--current) を下へ分割する｡現在の pane の内容には影響しない｡
func (h *herdrHost) SplitPane(ctx context.Context) (string, error) {
	out, err := h.run(ctx, "pane", "split", "--current", "--direction", "down", "--ratio", "0.2", "--no-focus")
	if err != nil {
		return "", fmt.Errorf("使い捨て pane の作成に失敗: %w", err)
	}
	var env struct {
		Result struct {
			Pane struct {
				PaneID string `json:"pane_id"`
			} `json:"pane"`
		} `json:"result"`
		Error *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(out), &env); err != nil {
		return "", fmt.Errorf("pane split の応答を解釈できません: %w (stdout=%q)", err, out)
	}
	if env.Error != nil {
		return "", fmt.Errorf("herdr pane split: %s (%s)", env.Error.Message, env.Error.Code)
	}
	if env.Result.Pane.PaneID == "" {
		return "", fmt.Errorf("pane split の応答に pane_id が含まれていません (stdout=%q)", out)
	}
	return env.Result.Pane.PaneID, nil
}

// MarkAsClaudeAgent は herdr pane report-agent で paneID を Claude agent に見せかける｡
func (h *herdrHost) MarkAsClaudeAgent(ctx context.Context, paneID string) (string, error) {
	if _, err := h.run(ctx, "pane", "report-agent", paneID, "--source", simulateMetadataSource, "--agent", "claude", "--state", "idle"); err != nil {
		return "", fmt.Errorf("pane %s を Claude agent として登録できませんでした: %w", paneID, err)
	}
	return "Claude agent として herdr に登録しました (agent=claude, state=idle)", nil
}

// WriteScreen は herdr pane run で paneID のシェルに path を cat させる｡send-text だと
// 文字ごとの入力になり､内容によってはシェルへのコマンドとして解釈されかねない
// (実機確認済み: `herdr pane run <id> cat <file>` なら静的な表示に留まる)｡
// herdr pane run はコマンド文字列を pane に入力するだけで Enter は押さない (実測)｡
func (h *herdrHost) WriteScreen(ctx context.Context, paneID, path string) error {
	if _, err := h.run(ctx, "pane", "run", paneID, "cat", path); err != nil {
		return fmt.Errorf("pane %s への画面テキスト書き込みに失敗: %w", paneID, err)
	}
	time.Sleep(h.keyDelay)
	if _, err := h.run(ctx, "pane", "send-keys", paneID, "enter"); err != nil {
		return fmt.Errorf("pane %s への enter 送信に失敗: %w", paneID, err)
	}
	return nil
}

func (h *herdrHost) ClosePane(ctx context.Context, paneID string) error {
	var errs []error
	if _, err := h.run(ctx, "pane", "release-agent", paneID, "--source", simulateMetadataSource, "--agent", "claude"); err != nil {
		errs = append(errs, err)
	}
	if _, err := h.run(ctx, "pane", "close", paneID); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func (h *herdrHost) CloseCommand(paneID string) string {
	return "herdr pane close " + paneID
}

// rexHost は rex の split / send / send-key / block close で使い捨て block を扱う｡
// rex には herdr の report-agent に当たるものが無く､Claude の判定は Claude Code 自身が出す
// OSC 7501 (`block call program_status` の record) に頼る｡そこで使い捨て block のシェルに
// 同じ OSC 7501 を printf させ､本番と同じ判定 (rexcli の toPane) を通す｡
type rexHost struct {
	bin      string
	ownBlock string
	keyDelay time.Duration
}

// claudeProgramStatusCommand は Claude Code 2.1.296 が入力待ちで出すものと同じ書式の OSC 7501｡
// payload は `key=value` を `:` で繋ぎ､終端は BEL｡printf に解釈させる形で送り､生の
// ESC / BEL を pty に流さない｡
const claudeProgramStatusCommand = `printf '\033]7501;state=idle:app=claude-code\a'`

// run は rex を実行する｡server が落ちているときに CLI が起こさないよう autostart を切る (rexcli と同じ)｡
func (h *rexHost) run(ctx context.Context, args ...string) ([]byte, error) {
	return runRaw(ctx, h.bin, append([]string{"--autostart=false"}, args...)...)
}

// SplitPane は自分の block (REX_BLOCK) の下にシェルの block を作る｡rex の capture は可視領域しか
// 返さないので､limit 風の画面とシェルのプロンプトが収まる高さ (40%) を取る｡
func (h *rexHost) SplitPane(ctx context.Context) (string, error) {
	out, err := h.run(ctx, "split", "-b", h.ownBlock, "--split=below", "--ratio", "40", "--focus=false", "--label", simulateMetadataSource, "--json")
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

// typeCommand は block のシェルに command を入力して Enter を送る｡
func (h *rexHost) typeCommand(ctx context.Context, blockID, command string) error {
	if _, err := h.run(ctx, "send", "-b", blockID, command); err != nil {
		return err
	}
	time.Sleep(h.keyDelay)
	_, err := h.run(ctx, "send-key", "-b", blockID, "enter")
	return err
}

func (h *rexHost) MarkAsClaudeAgent(ctx context.Context, paneID string) (string, error) {
	if err := h.typeCommand(ctx, paneID, claudeProgramStatusCommand); err != nil {
		return "", fmt.Errorf("block %s のシェルに OSC 7501 を出させられませんでした: %w", paneID, err)
	}
	return "シェルから OSC 7501 を出し､Claude Code を名乗らせました (app=claude-code, state=idle)", nil
}

func (h *rexHost) WriteScreen(ctx context.Context, paneID, path string) error {
	if err := h.typeCommand(ctx, paneID, "cat "+shellQuote(path)); err != nil {
		return fmt.Errorf("block %s への画面テキスト書き込みに失敗: %w", paneID, err)
	}
	return nil
}

func (h *rexHost) ClosePane(ctx context.Context, paneID string) error {
	_, err := h.run(ctx, "block", "close", paneID)
	return err
}

func (h *rexHost) CloseCommand(paneID string) string {
	return "rex block close " + paneID
}

// shellQuote は s をシングルクォートで包む｡中のシングルクォートは閉じて逃がして開き直す｡
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
