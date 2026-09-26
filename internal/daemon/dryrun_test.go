package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/usadamasa/agents-daemon/internal/applog"
	"github.com/usadamasa/agents-daemon/internal/herdrcli"
	"github.com/usadamasa/agents-daemon/internal/herdrcli/herdrclifake"
)

// dryRunClient は稼働中の Claude セッションへの誤送信を防ぐ安全弁そのものなので､
// 「読み取り系はそのまま inner へ通す」「書き込み系は inner に絶対到達しない」を
// 直接検証する｡inner 側の FakeClient に到達したかどうかを Calls で確認する｡

func TestDryRunClient_ReadOperationsPassThrough(t *testing.T) {
	inner := &herdrclifake.Client{
		PaneListFunc: func(context.Context) ([]herdrcli.Pane, error) {
			return []herdrcli.Pane{{PaneID: "p1"}}, nil
		},
		PaneGetFunc: func(context.Context, string) (herdrcli.Pane, error) {
			return herdrcli.Pane{PaneID: "p1"}, nil
		},
		PaneReadFunc: func(context.Context, string, herdrcli.PaneReadOptions) (string, error) {
			return "screen text", nil
		},
		ReachableFunc: func(context.Context) error {
			return nil
		},
	}
	log := applog.NewLogger(filepath.Join(t.TempDir(), "daemon.log"), time.Now)
	defer func() { _ = log.Close() }()
	c := newDryRunClient(inner, log)
	ctx := context.Background()

	t.Run("PaneListはinnerへ到達する", func(t *testing.T) {
		panes, err := c.PaneList(ctx)
		if err != nil || len(panes) != 1 || panes[0].PaneID != "p1" {
			t.Errorf("PaneList() = %v, %v, want [{p1}], nil", panes, err)
		}
	})

	t.Run("PaneGetはinnerへ到達する", func(t *testing.T) {
		pane, err := c.PaneGet(ctx, "p1")
		if err != nil || pane.PaneID != "p1" {
			t.Errorf("PaneGet() = %v, %v, want {p1}, nil", pane, err)
		}
	})

	t.Run("PaneReadはinnerへ到達する", func(t *testing.T) {
		screen, err := c.PaneRead(ctx, "p1", herdrcli.PaneReadOptions{})
		if err != nil || screen != "screen text" {
			t.Errorf("PaneRead() = %q, %v, want %q, nil", screen, err, "screen text")
		}
	})

	t.Run("Reachableはinnerへ到達する", func(t *testing.T) {
		if err := c.Reachable(ctx); err != nil {
			t.Errorf("Reachable() error = %v, want nil", err)
		}
	})

	want := []string{"PaneList", "PaneGet:p1", "PaneRead:p1", "Reachable"}
	if got := inner.Calls; len(got) != len(want) {
		t.Fatalf("inner.Calls = %v, want %v", got, want)
	} else {
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("inner.Calls[%d] = %q, want %q", i, got[i], want[i])
			}
		}
	}
}

func TestDryRunClient_WriteOperationsNeverReachInner(t *testing.T) {
	inner := &herdrclifake.Client{
		SendTextFunc: func(context.Context, string, string) error {
			return errors.New("実際のペインへ送信されてしまった")
		},
		SendKeysFunc: func(context.Context, string, ...string) error {
			return errors.New("実際のペインへ送信されてしまった")
		},
		ReportMetadataFunc: func(context.Context, string, herdrcli.ReportMetadataOptions) error {
			return errors.New("実際のペインが更新されてしまった")
		},
	}
	logPath := filepath.Join(t.TempDir(), "daemon.log")
	log := applog.NewLogger(logPath, time.Now)
	c := newDryRunClient(inner, log)
	ctx := context.Background()

	t.Run("SendTextはinnerに到達せず常に成功を返す", func(t *testing.T) {
		if err := c.SendText(ctx, "p1", "Continue where you left off."); err != nil {
			t.Errorf("SendText() error = %v, want nil (inner に到達していたら non-nil のはず)", err)
		}
	})

	t.Run("SendKeysはinnerに到達せず常に成功を返す", func(t *testing.T) {
		if err := c.SendKeys(ctx, "p1", "esc", "enter"); err != nil {
			t.Errorf("SendKeys() error = %v, want nil", err)
		}
	})

	t.Run("ReportMetadataはinnerに到達せず常に成功を返す", func(t *testing.T) {
		if err := c.ReportMetadata(ctx, "p1", herdrcli.ReportMetadataOptions{Source: metadataSource}); err != nil {
			t.Errorf("ReportMetadata() error = %v, want nil", err)
		}
	})

	if len(inner.Calls) != 0 {
		t.Fatalf("inner.Calls = %v, want 空 (書き込み系は inner に到達してはいけない)", inner.Calls)
	}

	if err := log.Close(); err != nil {
		t.Fatalf("log.Close() error = %v", err)
	}
	data, err := os.ReadFile(logPath) // #nosec G304 -- テストの一時ディレクトリ配下
	if err != nil {
		t.Fatalf("ログファイルが読めない: %v", err)
	}
	logged := string(data)
	for _, want := range []string{"p1 へテキストを送信しない", "p1 へキーを送信しない", "p1 のラベルを更新しない"} {
		if !strings.Contains(logged, want) {
			t.Errorf("ログに %q が含まれていない: %q", want, logged)
		}
	}
}
