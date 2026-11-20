// Package rex は rex を操作する multiplexer.Client の実装 (adapter) を提供する。rex の語彙を daemon の語彙 (Pane、
// Geometry) へ写す。rex は control API (api) だけで操作し、rex の CLI は起こさない。購読の cache は watch、doctor の
// 前提点検は premise、spawn した pane の中で走る包み (包みだけが CLI を叩く) は wrap、応答と event の形は wire、
// block の状態から Pane への写像は blockstate にある。
package rex

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/usadamasa/agents-daemon/internal/multiplexer"
	"github.com/usadamasa/agents-daemon/internal/runtime/backend/rex/api"
	"github.com/usadamasa/agents-daemon/internal/runtime/backend/rex/blockstate"
	"github.com/usadamasa/agents-daemon/internal/runtime/backend/rex/premise"
	"github.com/usadamasa/agents-daemon/internal/runtime/backend/rex/watch"
	"github.com/usadamasa/agents-daemon/internal/runtime/backend/rex/wire"
)

// defaultTimeout は control API の request 1 回あたりのタイムアウト。
const defaultTimeout = 10 * time.Second

// SessionResolver は block の ID から Claude Code の session_id への対応を返す。
// 実体は statusline が `context/<sid>.json` の multiplexer に書いた記録 (sessioncontext.Store.ListMultiplexerBlocks)。
// PaneList / PaneGet の呼び出しごとに 1 回だけ呼ぶ。
type SessionResolver func() (map[string]string, error)

// execClient は rex を操作する multiplexer.Client 実装。
type execClient struct {
	// rex は control API を呼ぶ。
	rex *api.Client
	// resolve は block_id から session_id を引く (statusline が書いた sidecar)。
	resolve SessionResolver
	// watch は Watch が始めた購読の cache。nil なら購読しておらず、PaneList は毎回全列挙する。
	watch *watch.Watcher
	// watchBackoff は購読が切れてから張り直すまでの最初の間隔。0 なら defaultWatchBackoff。
	watchBackoff time.Duration
}

// NewExecClient は rex server を操作する Client を返す。control API の socket は REX_SERVER か既定の場所。
// 接続は最初の呼び出しで張る。
func NewExecClient(resolve SessionResolver) multiplexer.Client {
	return newExecClientWithTimeout(resolve, defaultTimeout)
}

func newExecClientWithTimeout(resolve SessionResolver, timeout time.Duration) *execClient {
	return &execClient{rex: api.New(api.DefaultSocket, timeout), resolve: resolve}
}

// PaneList は shell の block を block ID の順に返す。Watch の購読が同期していれば cache から返し、rex を叩かない。
// 同期していなければ (購読していない、張り直しの途中) 全列挙する。
func (c *execClient) PaneList(ctx context.Context) ([]multiplexer.Pane, error) {
	sessions, err := c.resolve()
	if err != nil {
		return nil, fmt.Errorf("block と session の対応の読み込みに失敗: %w", err)
	}
	if panes, ok := c.watch.Panes(sessions); ok {
		return panes, nil
	}
	cache, err := c.snapshot(ctx)
	if err != nil {
		return nil, err
	}
	return cache.Panes(sessions), nil
}

// snapshot は全 session の shell の block に program_status と process を打ち、session ごとの view を読む。
// 1 回で session.list 1 回、session ごとに list_blocks と session.view を 1 回ずつ、shell の block ごとに 2 回の request になる。
func (c *execClient) snapshot(ctx context.Context) (*blockstate.Cache, error) {
	sessions, err := c.rex.ListSessions(ctx)
	if err != nil {
		return nil, err
	}
	blocks := map[string]*blockstate.State{}
	views := map[string]wire.SessionView{}
	failures := map[string]error{}
	for _, session := range sessions {
		shells, err := c.shellBlocks(ctx, session)
		if err != nil {
			// list の後に閉じた session と閉じかけの session (conflict) は読み飛ばす。block の場合と同じく、失敗を
			// 返すと session の close だけで daemon が PaneList の連続失敗に数える。
			if isNotFound(err) || api.IsConflict(err) {
				continue
			}
			return nil, err
		}
		view, ok, failure := c.view(ctx, session)
		if ok {
			views[session] = view
		}
		if failure != nil {
			failures[session] = failure
		}
		for _, id := range shells {
			st, err := c.fetchBlock(ctx, session, id)
			if err != nil {
				// list_blocks の後に閉じた block (閉じかけの session の conflict を含む) は読み飛ばす。ここで失敗を
				// 返すと daemon が PaneList の連続失敗に数え、block の開け閉めだけで終了しかねない。
				if isNotFound(err) || api.IsConflict(err) {
					continue
				}
				return nil, err
			}
			blocks[id] = st
		}
	}
	cache := blockstate.NewCache(blocks)
	for session, view := range views {
		cache.PutView(session, view)
	}
	for session, failure := range failures {
		cache.PutViewFailure(session, failure)
	}
	return cache, nil
}

// view は session の view を読む。読めれば ok が true。閉じた session と閉じかけの session (not found、conflict) は
// ok も failure も空で、それ以外の失敗 (応答の形の崩れを含む) は failure に返し、呼び手が pane の LocationReason に
// 載せる。置き場は依存図のまとめ方にしか使わないので、読めないことで PaneList を失敗にしない (daemon が連続失敗に
// 数えて終了しかねない)。
func (c *execClient) view(ctx context.Context, session string) (view wire.SessionView, ok bool, failure error) {
	view, err := c.rex.SessionView(ctx, session)
	switch {
	case err == nil:
		return view, true, nil
	case isNotFound(err) || api.IsConflict(err):
		return wire.SessionView{}, false, nil
	default:
		return wire.SessionView{}, false, err
	}
}

// shellBlocks は session の shell の block の ID を返す。これ以外の block は program_status を持たない。
func (c *execClient) shellBlocks(ctx context.Context, session string) ([]string, error) {
	blocks, err := c.rex.ListBlocks(ctx, session)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, b := range blocks.Blocks {
		if b.Flavor == wire.ShellFlavor {
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
	session, err := c.rex.ResolveBlock(ctx, paneID)
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
func (c *execClient) fetchBlock(ctx context.Context, session, blockID string) (*blockstate.State, error) {
	ps, err := c.rex.ProgramStatus(ctx, session, blockID)
	if err != nil {
		return nil, err
	}
	proc, err := c.rex.Process(ctx, session, blockID)
	if err != nil {
		return nil, err
	}
	return &blockstate.State{Session: session, ProgramStatus: ps, Process: proc}, nil
}

// PaneRead は block の画面を返す。Claude Code は alternate screen で描くので、Claude の block では
// 可視の行数で頭打ちになる (alternate screen でない block では scrollback も返る)。
// opts.Lines > 0 なら末尾 Lines 行に絞る。opts.Styled なら制御シーケンスを保つ。
// trim と unwrap は CLI の capture --trim --unwrap と同じ args で、画面が改行で終わっていなければ改行を 1 つ足して
// capture と同じ形にする (tailLines と検知は末尾の改行のある画面で書いた)。出力は capture とバイト単位で一致する
// (2026-11-17 に text と vt の両方で実測)。
func (c *execClient) PaneRead(ctx context.Context, paneID string, opts multiplexer.PaneReadOptions) (string, error) {
	session, err := c.rex.ResolveBlock(ctx, paneID)
	if err != nil {
		return "", err
	}
	out, err := c.rex.Format(ctx, session, paneID, api.FormatOptions{Trim: true, Unwrap: true, VT: opts.Styled})
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

// PaneGeometry は size と modes の 2 回で block の高さと screen を返す。
func (c *execClient) PaneGeometry(ctx context.Context, paneID string) (multiplexer.Geometry, error) {
	session, err := c.rex.ResolveBlock(ctx, paneID)
	if err != nil {
		return multiplexer.Geometry{}, err
	}
	size, err := c.rex.Size(ctx, session, paneID)
	if err != nil {
		return multiplexer.Geometry{}, err
	}
	modes, err := c.rex.Modes(ctx, session, paneID)
	if err != nil {
		return multiplexer.Geometry{}, err
	}
	return multiplexer.Geometry{Rows: size.Rows, Columns: size.Columns, AlternateScreen: modes.Screen == wire.ScreenAlternate}, nil
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

// CheckPremises は doctor の前提点検 (multiplexer.PremiseChecker)。点検の中身は premise が持つ。
func (c *execClient) CheckPremises(ctx context.Context, blockID, sessionID string) []multiplexer.PremiseFinding {
	return premise.Check(ctx, c.rex, rexBinary, blockID, sessionID)
}

// Reachable は server.status で rex server の生死を確かめる。
func (c *execClient) Reachable(ctx context.Context) error {
	st, err := c.rex.ServerStatus(ctx)
	if err != nil {
		return err
	}
	if st.PID <= 0 {
		return errors.New("rex server の pid を取れない")
	}
	return nil
}
