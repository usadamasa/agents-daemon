// Package focus は、Claude Code のセッションが動いている rex の pane の居る window を、Rex.app に表示させる (#375)。
//
// 依存図のアプリ (gui/) は rex の外にあり、利用者はセッションの pane へ行くのに session と window を探すことになる。
// アプリのボタンが `agents-daemon focus` を呼び、Rex.app の表示をその window へ切り替える。多重化ツールへの操作は
// mod や hook から叩かない決めごとどおり daemon のバイナリが担う。
//
// tell と違い、claude が前景かは問わない。人の手が要る pane (prompt が開いている、claude が落ちて shell に戻った) へ
// 行く手段なので、pane の状態で断ると要る場面で使えなくなる。
//
// 多重化ツールの口 (Host) は multiplexer.Client に足さず、ここに閉じる (tell.Host と同じ考え)。
package focus

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/usadamasa/agents-daemon/internal/protocol"
)

// Host は block の居る session と window を引き、その window の表示を頼む口。rex の backend が持つ。
type Host interface {
	// ResolveBlock は block の居る session の ID。閉じた block は失敗する。
	ResolveBlock(ctx context.Context, blockID string) (string, error)
	// WindowOf は block の居る window の ID。window に置かれていない block は失敗する。
	WindowOf(ctx context.Context, session, blockID string) (windowID, label string, err error)
	// ShowWindow は利用者の見ているアプリに session の window を表示させる。
	ShowWindow(ctx context.Context, session, windowID string) error
}

// Request は表示させるセッション。
type Request struct {
	// ToSession は Claude Code のセッションの ID。
	ToSession string
}

// Result は表示させた先。JSON で stdout に出す。形は protocol の schema が持つ。
type Result = protocol.FocusResult

// Runner は表示させる手段。テストは Host と Blocks を差し替える。
type Runner struct {
	Host Host
	// Blocks は block の ID から session_id への対応 (sessioncontext.Store.ListMultiplexerBlocks)。
	Blocks func() (map[string]string, error)
}

// Run は req のセッションの block の居る window を表示させる。block が複数あれば新しいもの (block の ID は時刻順) から
// 試し、閉じた block と window に置かれていない block は飛ばす。表示を断られたら、ほかの block では頼み直さない
// (断る理由はアプリの側にあり、block を替えても変わらない)。
func (r Runner) Run(ctx context.Context, req Request) (Result, error) {
	blocks, err := r.Blocks()
	if err != nil {
		return Result{}, fmt.Errorf("block と session の対応を読めない: %w", err)
	}
	var ids []string
	for id, sid := range blocks {
		if sid == req.ToSession {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return Result{}, fmt.Errorf("セッション %s の pane が見つからない (statusline が context を書いていないか、終了している)", req.ToSession)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(ids)))
	var missing []error
	for _, id := range ids {
		session, window, err := r.locate(ctx, id)
		if err != nil {
			missing = append(missing, err)
			continue
		}
		if err := r.Host.ShowWindow(ctx, session, window); err != nil {
			return Result{}, err
		}
		return Result{PaneID: id, RexSessionID: session, WindowID: window}, nil
	}
	return Result{}, fmt.Errorf("セッション %s の pane の居る window が無い: %w", req.ToSession, errors.Join(missing...))
}

// locate は block の居る session と window を引く。
func (r Runner) locate(ctx context.Context, blockID string) (session, window string, err error) {
	session, err = r.Host.ResolveBlock(ctx, blockID)
	if err != nil {
		return "", "", fmt.Errorf("pane %s: %w", blockID, err)
	}
	window, _, err = r.Host.WindowOf(ctx, session, blockID)
	if err != nil {
		return "", "", fmt.Errorf("pane %s: %w", blockID, err)
	}
	return session, window, nil
}
