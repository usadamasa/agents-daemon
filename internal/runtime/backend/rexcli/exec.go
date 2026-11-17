// Package rexcli は rex を操作する multiplexer.Client の実装を提供する。
// 読み取りは rex server の control API (control) を api.go の接続から呼び、書き込みと events の購読は
// rex の CLI を fork する (exec.go、watch.go)。応答と events の形、block の状態から multiplexer.Pane への写像は
// blockstate、doctor の前提点検は premise にある。
package rexcli

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/usadamasa/agents-daemon/internal/multiplexer"
	"github.com/usadamasa/agents-daemon/internal/runtime/backend/rexcli/blockstate"
	"github.com/usadamasa/agents-daemon/internal/runtime/backend/rexcli/cli"
	"github.com/usadamasa/agents-daemon/internal/runtime/backend/rexcli/control"
	"github.com/usadamasa/agents-daemon/internal/runtime/backend/rexcli/premise"
)

// defaultTimeout は rex の呼び出し 1 回あたりのタイムアウト (CLI の起動と API の request のどちらも)。
const defaultTimeout = 10 * time.Second

// SessionResolver は block の ID から Claude Code の session_id への対応を返す。
// 実体は statusline が `context/<sid>.json` の multiplexer に書いた記録 (sessioncontext.Store.ListMultiplexerBlocks)。
// PaneList / PaneGet の呼び出しごとに 1 回だけ呼ぶ。
type SessionResolver func() (map[string]string, error)

// execClient は rex を操作する multiplexer.Client 実装。
type execClient struct {
	// cli は書き込み系と events の購読で rex の CLI を起こす。
	cli *cli.Runner
	// timeout は control API の request 1 回の制限時間。0 以下なら課さない (テスト用)。
	timeout time.Duration
	// resolve は block_id から session_id を引く (statusline が書いた sidecar)。
	resolve SessionResolver
	// watch は Watch が始めた購読の cache。nil なら購読しておらず、PaneList は毎回全列挙する。
	watch *watchState
	// watchBackoff は購読が切れてから張り直すまでの最初の間隔。0 なら defaultWatchBackoff。
	watchBackoff time.Duration
	// socket は control API の socket のパスを返す。
	socket func() (string, error)
	// apiMu は api を守る。api は使い回す control 接続で、nil なら次の呼び出しで張る。
	apiMu sync.Mutex
	api   *control.Conn
}

// NewExecClient は実 rex バイナリを叩く Client を返す。
// バイナリは最初の呼び出しで PATH から引く。環境変数 REX_BIN_PATH があればそれで固定する。
func NewExecClient(resolve SessionResolver) multiplexer.Client {
	return newExecClientWithTimeout(resolve, defaultTimeout)
}

func newExecClientWithTimeout(resolve SessionResolver, timeout time.Duration) *execClient {
	return &execClient{cli: cli.FromEnv(timeout), timeout: timeout, resolve: resolve, socket: defaultSocket}
}

type lsResult struct {
	Sessions []struct {
		SessionID string `json:"session_id"`
	} `json:"sessions"`
}

// PaneList は shell の block を block ID の順に返す。Watch の購読が同期していれば cache から返し、rex を叩かない。
// 同期していなければ (購読していない、張り直しの途中) 全列挙する。
func (c *execClient) PaneList(ctx context.Context) ([]multiplexer.Pane, error) {
	sessions, err := c.resolve()
	if err != nil {
		return nil, fmt.Errorf("block と session の対応の読み込みに失敗: %w", err)
	}
	if panes, ok := c.watch.panes(sessions); ok {
		return panes, nil
	}
	blocks, err := c.snapshot(ctx)
	if err != nil {
		return nil, err
	}
	return blockstate.NewCache(blocks).Panes(sessions), nil
}

// snapshot は全 session の shell の block に program_status と process を打つ。
// 1 回で session.list 1 回、session ごとに list_blocks 1 回、shell の block ごとに 2 回の request になる。
func (c *execClient) snapshot(ctx context.Context) (map[string]*blockstate.State, error) {
	var ls lsResult
	if err := c.call(ctx, "session.list", nil, &ls); err != nil {
		return nil, err
	}
	blocks := map[string]*blockstate.State{}
	for _, s := range ls.Sessions {
		shells, err := c.shellBlocks(ctx, s.SessionID)
		if err != nil {
			// list の後に閉じた session は読み飛ばす。block の場合と同じく、失敗を返すと session の close だけで
			// daemon が PaneList の連続失敗に数える。
			if isNotFound(err) {
				continue
			}
			return nil, err
		}
		for _, id := range shells {
			st, err := c.fetchBlock(ctx, s.SessionID, id)
			if err != nil {
				// list_blocks の後に閉じた block は読み飛ばす。ここで失敗を返すと daemon が
				// PaneList の連続失敗に数え、block の開け閉めだけで終了しかねない。
				if isNotFound(err) {
					continue
				}
				return nil, err
			}
			blocks[id] = st
		}
	}
	return blocks, nil
}

// listBlocks は session の block を flavor を問わず返す。
// rex-premise: preview.30 api session.list_blocks は session の全 window と detached の block を返し、無い session は session_not_found
func (c *execClient) listBlocks(ctx context.Context, session string) (blockstate.BlockList, error) {
	var blocks blockstate.BlockList
	err := c.call(ctx, "session.list_blocks", map[string]any{"session_id": session}, &blocks)
	return blocks, err
}

// shellBlocks は session の shell の block の ID を返す。これ以外の block は program_status を持たない。
func (c *execClient) shellBlocks(ctx context.Context, session string) ([]string, error) {
	blocks, err := c.listBlocks(ctx, session)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, b := range blocks.Blocks {
		if b.Flavor == blockstate.ShellFlavor {
			ids = append(ids, b.BlockID)
		}
	}
	return ids, nil
}

func (c *execClient) PaneGet(ctx context.Context, paneID string) (multiplexer.Pane, error) {
	sessions, err := c.resolve()
	if err != nil {
		return multiplexer.Pane{}, fmt.Errorf("block と session の対応の読み込みに失敗: %w", err)
	}
	session, err := c.blockSession(ctx, paneID)
	if err != nil {
		return multiplexer.Pane{}, err
	}
	st, err := c.fetchBlock(ctx, session, paneID)
	if err != nil {
		return multiplexer.Pane{}, err
	}
	return blockstate.ToPane(paneID, st.ProgramStatus, st.Process, sessions[paneID]), nil
}

// fetchBlock は 1 block に program_status と process を打つ。
// rex-premise: preview.30 api program_status と process の結果は CLI の block call と同じ形
func (c *execClient) fetchBlock(ctx context.Context, session, blockID string) (*blockstate.State, error) {
	st := &blockstate.State{Session: session}
	if err := c.blockCall(ctx, session, blockID, "program_status", nil, &st.ProgramStatus); err != nil {
		return nil, err
	}
	if err := c.blockCall(ctx, session, blockID, "process", nil, &st.Process); err != nil {
		return nil, err
	}
	return st, nil
}

// PaneRead は block の画面を返す。Claude Code は alternate screen で描くので、Claude の block では
// 可視の行数で頭打ちになる (alternate screen でない block では scrollback も返る)。
// opts.Lines > 0 なら末尾 Lines 行に絞る。opts.Styled なら制御シーケンスを保つ。
// 画面が改行で終わっていなければ改行を 1 つ足し、CLI の capture と同じ形にする (tailLines と検知は末尾の改行のある
// 画面で書いた)。format の trim は行末の空白を消す。CLI の capture --trim も同じ trim を送っており、出力は content と
// 改行 1 つでバイト単位で一致する (2026-11-17 に text と vt の両方で実測)。
// rex-premise: preview.30 api format は alternate screen では可視の行だけ、通常の画面では scrollback も返す。trim の content は改行で終わらない
// rex-premise: preview.30 api format の vt は制御シーケンス (SGR、CR) を保つ
func (c *execClient) PaneRead(ctx context.Context, paneID string, opts multiplexer.PaneReadOptions) (string, error) {
	session, err := c.blockSession(ctx, paneID)
	if err != nil {
		return "", err
	}
	args := map[string]any{"trim": true, "unwrap": true, "max_bytes": formatChunkBytes}
	if opts.Styled {
		args["format"] = "vt"
	}
	out, err := c.readFormat(ctx, session, paneID, args)
	if err != nil {
		return "", err
	}
	if !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	if opts.Lines > 0 {
		return tailLines(out, opts.Lines), nil
	}
	return out, nil
}

type sizeResult struct {
	Rows    int `json:"rows"`
	Columns int `json:"columns"`
}

type modesResult struct {
	Screen string `json:"screen"`
}

// PaneGeometry は size と modes の 2 回で block の高さと screen を返す。modes は idle の terminal を起こさない
// (changelog)。
// rex-premise: preview.30 api size は rows と columns を返す
// rex-premise: preview.30 api modes は screen (primary / alternate) を返す
func (c *execClient) PaneGeometry(ctx context.Context, paneID string) (multiplexer.Geometry, error) {
	session, err := c.blockSession(ctx, paneID)
	if err != nil {
		return multiplexer.Geometry{}, err
	}
	var size sizeResult
	if err := c.blockCall(ctx, session, paneID, "size", nil, &size); err != nil {
		return multiplexer.Geometry{}, err
	}
	var modes modesResult
	if err := c.blockCall(ctx, session, paneID, "modes", nil, &modes); err != nil {
		return multiplexer.Geometry{}, err
	}
	return multiplexer.Geometry{Rows: size.Rows, Columns: size.Columns, AlternateScreen: modes.Screen == blockstate.ScreenAlternate}, nil
}

// tailLines は s の末尾 n 行を返す。末尾の改行は行に数えず、あればそのまま残す。
func tailLines(s string, n int) string {
	body, hasNL := strings.CutSuffix(s, "\n")
	lines := strings.Split(body, "\n")
	if len(lines) <= n {
		return s
	}
	tail := strings.Join(lines[len(lines)-n:], "\n")
	if hasNL {
		tail += "\n"
	}
	return tail
}

func (c *execClient) SendText(ctx context.Context, paneID, text string) error {
	_, err := c.cli.Run(ctx, "send", "-b", paneID, text)
	return err
}

func (c *execClient) SendKeys(ctx context.Context, paneID string, keys ...string) error {
	_, err := c.cli.Run(ctx, append([]string{"send-key", "-b", paneID}, keys...)...)
	return err
}

type serverStatusResult struct {
	PID int `json:"pid"`
}

// CheckPremises は doctor の前提点検 (multiplexer.PremiseChecker)。点検の中身は premise が持ち、rex はこの Client と
// 同じ呼び方で打つ。
func (c *execClient) CheckPremises(ctx context.Context, blockID, sessionID string) []multiplexer.PremiseFinding {
	return premise.Check(ctx, premiseRunner{c.cli}, blockID, sessionID)
}

// premiseRunner は CLI の Runner に exit 3 の判定を足して premise.Runner にする。
type premiseRunner struct{ *cli.Runner }

func (premiseRunner) TargetNotFound(err error) bool { return cli.TargetNotFound(err) }

// Reachable は server.status で rex server の生死を確かめる。
func (c *execClient) Reachable(ctx context.Context) error {
	var st serverStatusResult
	if err := c.call(ctx, "server.status", nil, &st); err != nil {
		return err
	}
	if st.PID <= 0 {
		return errors.New("rex server の pid を取れない")
	}
	return nil
}
