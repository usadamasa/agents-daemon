// Package muxfake は mux.Client のテスト用実装を提供する｡mux 本体から切り出して
// いるのは､これがテスト scaffolding であり､mux の公開 API (実運用で使う型・関数) とは
// 性質が異なるため｡daemon / verify / monitor いずれのテストも､実クライアントを
// 一切経由せずにこのパッケージだけを import すればよい｡
package muxfake

import (
	"context"
	"fmt"

	"github.com/usadamasa/agents-daemon/internal/mux"
)

// Client は mux.Client のテスト用実装｡各メソッドの挙動を *Func フィールドで
// 差し替えられる｡
type Client struct {
	PaneListFunc       func(ctx context.Context) ([]mux.Pane, error)
	PaneGetFunc        func(ctx context.Context, paneID string) (mux.Pane, error)
	PaneReadFunc       func(ctx context.Context, paneID string, opts mux.PaneReadOptions) (string, error)
	SendTextFunc       func(ctx context.Context, paneID, text string) error
	SendKeysFunc       func(ctx context.Context, paneID string, keys ...string) error
	ReportMetadataFunc func(ctx context.Context, paneID string, opts mux.ReportMetadataOptions) error
	ReachableFunc      func(ctx context.Context) error

	// Calls は呼ばれた操作の履歴｡テストの assertion に使う｡
	Calls []string
	// SentTexts は SendText へ渡された文字列の履歴｡何を送ったかを問う
	// テスト (どの文面を投入したか) が Calls では区別できないため別に持つ｡
	SentTexts []string
}

var _ mux.Client = (*Client)(nil)

func (f *Client) PaneList(ctx context.Context) ([]mux.Pane, error) {
	f.Calls = append(f.Calls, "PaneList")
	if f.PaneListFunc != nil {
		return f.PaneListFunc(ctx)
	}
	return nil, nil
}

func (f *Client) PaneGet(ctx context.Context, paneID string) (mux.Pane, error) {
	f.Calls = append(f.Calls, "PaneGet:"+paneID)
	if f.PaneGetFunc != nil {
		return f.PaneGetFunc(ctx, paneID)
	}
	return mux.Pane{}, fmt.Errorf("muxfake.Client: PaneGetFunc が未設定")
}

func (f *Client) PaneRead(ctx context.Context, paneID string, opts mux.PaneReadOptions) (string, error) {
	f.Calls = append(f.Calls, "PaneRead:"+paneID)
	if f.PaneReadFunc != nil {
		return f.PaneReadFunc(ctx, paneID, opts)
	}
	return "", nil
}

func (f *Client) SendText(ctx context.Context, paneID, text string) error {
	f.Calls = append(f.Calls, "SendText:"+paneID)
	f.SentTexts = append(f.SentTexts, text)
	if f.SendTextFunc != nil {
		return f.SendTextFunc(ctx, paneID, text)
	}
	return nil
}

func (f *Client) SendKeys(ctx context.Context, paneID string, keys ...string) error {
	f.Calls = append(f.Calls, "SendKeys:"+paneID)
	if f.SendKeysFunc != nil {
		return f.SendKeysFunc(ctx, paneID, keys...)
	}
	return nil
}

func (f *Client) ReportMetadata(ctx context.Context, paneID string, opts mux.ReportMetadataOptions) error {
	f.Calls = append(f.Calls, "ReportMetadata:"+paneID)
	if f.ReportMetadataFunc != nil {
		return f.ReportMetadataFunc(ctx, paneID, opts)
	}
	return nil
}

func (f *Client) Reachable(ctx context.Context) error {
	f.Calls = append(f.Calls, "Reachable")
	if f.ReachableFunc != nil {
		return f.ReachableFunc(ctx)
	}
	return nil
}
