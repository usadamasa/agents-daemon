package verify

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"

	"github.com/usadamasa/agents-daemon/internal/apppath"
	"github.com/usadamasa/agents-daemon/internal/herdrcli"
	"github.com/usadamasa/agents-daemon/internal/herdrcli/herdrclifake"
)

func TestScopedClient_所有pane以外への操作を拒否する(t *testing.T) {
	const owned = "wH:pOwned"
	const other = "wH:pOther"

	newFake := func() *herdrclifake.Client {
		return &herdrclifake.Client{
			PaneGetFunc: func(_ context.Context, paneID string) (herdrcli.Pane, error) {
				return herdrcli.Pane{PaneID: paneID}, nil
			},
		}
	}

	t.Run("PaneGet: 所有 pane は inner へ委譲する", func(t *testing.T) {
		fake := newFake()
		c := newScopedClient(fake, owned)
		if _, err := c.PaneGet(context.Background(), owned); err != nil {
			t.Fatalf("PaneGet(owned) error = %v", err)
		}
		if !containsCall(fake.Calls, "PaneGet:"+owned) {
			t.Errorf("inner.PaneGet が呼ばれていない: %v", fake.Calls)
		}
	})

	t.Run("PaneGet: 他 pane は拒否し inner を呼ばない", func(t *testing.T) {
		fake := newFake()
		c := newScopedClient(fake, owned)
		_, err := c.PaneGet(context.Background(), other)
		assertPaneNotOwned(t, err)
		if len(fake.Calls) != 0 {
			t.Errorf("拒否されたのに inner が呼ばれている: %v", fake.Calls)
		}
	})

	t.Run("PaneRead: 他 pane は拒否する", func(t *testing.T) {
		fake := newFake()
		c := newScopedClient(fake, owned)
		_, err := c.PaneRead(context.Background(), other, herdrcli.PaneReadOptions{})
		assertPaneNotOwned(t, err)
		if len(fake.Calls) != 0 {
			t.Errorf("拒否されたのに inner が呼ばれている: %v", fake.Calls)
		}
	})

	t.Run("SendText: 他 pane は拒否する (これが無いと実 Claude セッションへ誤送信しうる)", func(t *testing.T) {
		fake := newFake()
		c := newScopedClient(fake, owned)
		err := c.SendText(context.Background(), other, "Continue where you left off.")
		assertPaneNotOwned(t, err)
		if len(fake.Calls) != 0 {
			t.Errorf("拒否されたのに inner が呼ばれている: %v", fake.Calls)
		}
	})

	t.Run("SendText: 所有 pane は inner へ委譲する", func(t *testing.T) {
		fake := newFake()
		c := newScopedClient(fake, owned)
		if err := c.SendText(context.Background(), owned, "hello"); err != nil {
			t.Fatalf("SendText(owned) error = %v", err)
		}
		if !containsCall(fake.Calls, "SendText:"+owned) {
			t.Errorf("inner.SendText が呼ばれていない: %v", fake.Calls)
		}
	})

	t.Run("SendKeys: 他 pane は拒否する", func(t *testing.T) {
		fake := newFake()
		c := newScopedClient(fake, owned)
		err := c.SendKeys(context.Background(), other, "enter")
		assertPaneNotOwned(t, err)
		if len(fake.Calls) != 0 {
			t.Errorf("拒否されたのに inner が呼ばれている: %v", fake.Calls)
		}
	})

	t.Run("ReportMetadata: 他 pane は拒否する", func(t *testing.T) {
		fake := newFake()
		c := newScopedClient(fake, owned)
		err := c.ReportMetadata(context.Background(), other, herdrcli.ReportMetadataOptions{Source: "x"})
		assertPaneNotOwned(t, err)
		if len(fake.Calls) != 0 {
			t.Errorf("拒否されたのに inner が呼ばれている: %v", fake.Calls)
		}
	})

	t.Run("PaneList と Reachable は pane を特定しない読み取りなので常に inner へ委譲する", func(t *testing.T) {
		fake := newFake()
		c := newScopedClient(fake, owned)
		if _, err := c.PaneList(context.Background()); err != nil {
			t.Fatalf("PaneList() error = %v", err)
		}
		if err := c.Reachable(context.Background()); err != nil {
			t.Fatalf("Reachable() error = %v", err)
		}
		if !containsCall(fake.Calls, "PaneList") || !containsCall(fake.Calls, "Reachable") {
			t.Errorf("PaneList/Reachable が inner へ委譲されていない: %v", fake.Calls)
		}
	})
}

func TestSimulateDryRunClient_送信系はinnerを呼ばずログだけ出す(t *testing.T) {
	fake := &herdrclifake.Client{}
	var buf bytes.Buffer
	c := simulateDryRunClient{inner: fake, w: &buf}

	if err := c.SendText(context.Background(), "p1", "Continue where you left off."); err != nil {
		t.Fatalf("SendText() error = %v", err)
	}
	if err := c.SendKeys(context.Background(), "p1", "esc", "enter"); err != nil {
		t.Fatalf("SendKeys() error = %v", err)
	}
	if err := c.ReportMetadata(context.Background(), "p1", herdrcli.ReportMetadataOptions{Source: "x"}); err != nil {
		t.Fatalf("ReportMetadata() error = %v", err)
	}

	if len(fake.Calls) != 0 {
		t.Fatalf("dry-run なのに inner の送信系メソッドが呼ばれている: %v", fake.Calls)
	}
	out := buf.String()
	for _, want := range []string{"[dry-run]", "Continue where you left off.", "esc enter"} {
		if !strings.Contains(out, want) {
			t.Errorf("出力に %q が含まれていない。出力:\n%s", want, out)
		}
	}
}

func TestSimulateDryRunClient_読み取り系はinnerへ委譲する(t *testing.T) {
	fake := &herdrclifake.Client{
		PaneGetFunc: func(_ context.Context, paneID string) (herdrcli.Pane, error) {
			return herdrcli.Pane{PaneID: paneID}, nil
		},
	}
	c := simulateDryRunClient{inner: fake, w: &bytes.Buffer{}}

	if _, err := c.PaneList(context.Background()); err != nil {
		t.Fatalf("PaneList() error = %v", err)
	}
	if _, err := c.PaneGet(context.Background(), "p1"); err != nil {
		t.Fatalf("PaneGet() error = %v", err)
	}
	if _, err := c.PaneRead(context.Background(), "p1", herdrcli.PaneReadOptions{}); err != nil {
		t.Fatalf("PaneRead() error = %v", err)
	}
	if err := c.Reachable(context.Background()); err != nil {
		t.Fatalf("Reachable() error = %v", err)
	}
	for _, want := range []string{"PaneList", "PaneGet:p1", "PaneRead:p1", "Reachable"} {
		if !containsCall(fake.Calls, want) {
			t.Errorf("inner.%s が呼ばれていない: %v", want, fake.Calls)
		}
	}
}

func containsCall(calls []string, want string) bool {
	for _, c := range calls {
		if c == want {
			return true
		}
	}
	return false
}

func assertPaneNotOwned(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("所有していない pane への操作が拒否されなかった")
	}
	if !errors.Is(err, errPaneNotOwned) {
		t.Errorf("error = %v, want errPaneNotOwned を wrap したもの", err)
	}
}

// TestRunSimulate_RealHerdr は実 herdr に対する統合テスト。herdr が PATH に無い、
// server に到達できない、または HERDR_ENV=1 のセッション内で実行されていない
// (使い捨て pane を split する基準となる pane が無い) 場合は skip する。
//
// これは「simulate が本当に実 pane に対して検知〜送信の経路を通せる」ことを保証する
// 最終防衛線。実 Claude セッションへ触れないことはここでは確認しない — それは
// scopedClient のユニットテスト (所有 pane 以外への操作を全て拒否する) が
// 型と経路のレベルで保証しており、実行結果の観察に頼るより確実なため。
func TestRunSimulate_RealHerdr(t *testing.T) {
	if _, err := exec.LookPath("herdr"); err != nil {
		t.Skip("herdr が PATH に無いので skip する")
	}
	if os.Getenv("HERDR_ENV") != "1" {
		t.Skip("HERDR_ENV=1 のセッション内でないので skip する")
	}

	client := herdrcli.NewExecClient()
	ctx := context.Background()
	if err := client.Reachable(ctx); err != nil {
		t.Skipf("herdr server に到達できないので skip する: %v", err)
	}

	home := t.TempDir()
	var buf bytes.Buffer
	if err := RunSimulate(ctx, &buf, apppath.New(home, "", ""), SimulateOptions{}); err != nil {
		t.Fatalf("RunSimulate() error = %v\n出力:\n%s", err, buf.String())
	}
	out := buf.String()
	t.Logf("simulate 出力:\n%s", out)

	for _, want := range []string{
		"を作成しました",
		"Claude agent として herdr に登録しました",
		"1 回目の Tick → Outcome:",
		"を閉じました",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("出力に %q が含まれていない", want)
		}
	}

	// 後始末は「作った pane を閉じたか」までを見る。実際に pane list から消えるのは
	// herdr 側の反映待ちで、ここで待ち受けると時間に依存したテストになる
	// (pane 数の比較でも消滅のポーリングでも flaky になった)。close を呼んだことは
	// 上の出力検査で確認済みで、閉じる対象が自分の作った pane に限られることは
	// scopedClient のユニットテストが保証している。
	if createdPaneID(out) == "" {
		t.Errorf("出力から作成した pane ID を取り出せない:\n%s", out)
	}
}

var createdPanePattern = regexp.MustCompile(`pane (\S+) を作成しました`)

// createdPaneID は simulate の出力から、作成した使い捨て pane の ID を取り出す。
func createdPaneID(out string) string {
	m := createdPanePattern.FindStringSubmatch(out)
	if len(m) < 2 {
		return ""
	}
	return m[1]
}
