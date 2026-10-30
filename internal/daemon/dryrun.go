package daemon

import (
	"context"
	"strings"

	"github.com/usadamasa/agents-daemon/internal/applog"
	"github.com/usadamasa/agents-daemon/internal/multiplexer"
)

// dryRunClient は multiplexerClient のうち pane を書き換える操作だけを握りつぶし、
// 「本来なら何を送っていたか」をログに残すデコレータ。読み取り (pane list /
// pane read / 端末の大きさ / 到達性確認 / 購読) はそのまま通すので、検知と判定は本番と同じ経路を通る。
//
// 実環境で初めて daemon を回すときの安全弁として用意している。稼働中の
// Claude セッションへ誤ってプロンプトを打ち込む事故は取り返しがつかないため、
// 「送信だけしない」状態を作れることを検証手順の入口に据えている。
type dryRunClient struct {
	inner multiplexerClient
	log   *applog.Logger
}

func newDryRunClient(inner multiplexerClient, log *applog.Logger) multiplexerClient {
	return dryRunClient{inner: inner, log: log}
}

func (c dryRunClient) PaneList(ctx context.Context) ([]multiplexer.Pane, error) {
	return c.inner.PaneList(ctx)
}

func (c dryRunClient) PaneRead(ctx context.Context, paneID string, opts multiplexer.PaneReadOptions) (string, error) {
	return c.inner.PaneRead(ctx, paneID, opts)
}

func (c dryRunClient) PaneGeometry(ctx context.Context, paneID string) (multiplexer.Geometry, error) {
	return c.inner.PaneGeometry(ctx, paneID)
}

func (c dryRunClient) Reachable(ctx context.Context) error {
	return c.inner.Reachable(ctx)
}

// Watch は購読 (読み取り) なので inner へ通す。inner が購読できなければ何もせず、閉じた channel を返す。
func (c dryRunClient) Watch(ctx context.Context, logf func(format string, args ...any)) <-chan struct{} {
	if w, ok := c.inner.(multiplexer.Watcher); ok {
		return w.Watch(ctx, logf)
	}
	done := make(chan struct{})
	close(done)
	return done
}

func (c dryRunClient) SendText(_ context.Context, paneID, text string) error {
	c.log.Logf("[dry-run] pane %s へテキストを送信しない: %q", paneID, text)
	return nil
}

func (c dryRunClient) SendKeys(_ context.Context, paneID string, keys ...string) error {
	c.log.Logf("[dry-run] pane %s へキーを送信しない: %s", paneID, strings.Join(keys, " "))
	return nil
}
