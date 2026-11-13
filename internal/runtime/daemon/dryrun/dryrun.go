// Package dryrun は daemon の多重化ツールの操作のうち pane を書き換えるものだけを握りつぶし、「本来なら何を
// 送っていたか」をログに残すデコレータを持つ。読み取り (pane list / pane read / 端末の大きさ / 到達性確認 / 購読) は
// そのまま通すので、検知と判定は本番と同じ経路を通る。
//
// 実環境で初めて daemon を回すときの安全弁として用意している。稼働中の Claude セッションへ誤ってプロンプトを
// 打ち込む事故は取り返しがつかないため、「送信だけしない」状態を作れることを検証手順の入口に据えている。
package dryrun

import (
	"context"
	"strings"

	"github.com/usadamasa/agents-daemon/internal/applog"
	"github.com/usadamasa/agents-daemon/internal/multiplexer"
	"github.com/usadamasa/agents-daemon/internal/runtime/monitor"
)

// Client は包む操作。daemon が多重化ツールに対して使う操作 (daemon の multiplexerClient) と同じ。
type Client interface {
	monitor.Client
	PaneList(ctx context.Context) ([]multiplexer.Pane, error)
	PaneGeometry(ctx context.Context, paneID string) (multiplexer.Geometry, error)
	Reachable(ctx context.Context) error
}

type client struct {
	inner Client
	log   *applog.Logger
}

// New は inner の書き込みを握りつぶして log に残す Client を返す。
func New(inner Client, log *applog.Logger) Client {
	return client{inner: inner, log: log}
}

func (c client) PaneList(ctx context.Context) ([]multiplexer.Pane, error) {
	return c.inner.PaneList(ctx)
}

func (c client) PaneRead(ctx context.Context, paneID string, opts multiplexer.PaneReadOptions) (string, error) {
	return c.inner.PaneRead(ctx, paneID, opts)
}

func (c client) PaneGeometry(ctx context.Context, paneID string) (multiplexer.Geometry, error) {
	return c.inner.PaneGeometry(ctx, paneID)
}

func (c client) Reachable(ctx context.Context) error {
	return c.inner.Reachable(ctx)
}

// Watch は購読 (読み取り) なので inner へ通す。inner が購読できなければ何もせず、閉じた channel を返す。
func (c client) Watch(ctx context.Context, logf func(format string, args ...any)) <-chan struct{} {
	if w, ok := c.inner.(multiplexer.Watcher); ok {
		return w.Watch(ctx, logf)
	}
	done := make(chan struct{})
	close(done)
	return done
}

func (c client) SendText(_ context.Context, paneID, text string) error {
	c.log.Logf("[dry-run] pane %s へテキストを送信しない: %q", paneID, text)
	return nil
}

func (c client) SendKeys(_ context.Context, paneID string, keys ...string) error {
	c.log.Logf("[dry-run] pane %s へキーを送信しない: %s", paneID, strings.Join(keys, " "))
	return nil
}
