package simulate

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
	"github.com/usadamasa/agents-daemon/internal/multiplexer"
	"github.com/usadamasa/agents-daemon/internal/multiplexer/multiplexerfake"
)

func TestRunSimulate_前提が無ければ止める(t *testing.T) {
	cases := []struct {
		name    string
		session string
		block   string
		want    string
	}{
		{"rex の外", "", "", "REX_SESSION"},
		{"rex の中だが REX_BLOCK が無い", "session:01a12439-0839-7829-a4cb-aeac453570c1", "", "REX_BLOCK"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("REX_SESSION", tc.session)
			t.Setenv("REX_BLOCK", tc.block)
			var buf bytes.Buffer
			err := Run(context.Background(), &buf, apppath.New(t.TempDir(), "", ""), Options{})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Run() error = %v, want %s に触れるエラー", err, tc.want)
			}
			if buf.Len() != 0 {
				t.Errorf("止める前に何かした: %s", buf.String())
			}
		})
	}
}

func TestScopedClient_所有pane以外への操作を拒否する(t *testing.T) {
	const owned = "wH:pOwned"
	const other = "wH:pOther"

	newFake := func() *multiplexerfake.Client {
		return &multiplexerfake.Client{
			PaneGetFunc: func(_ context.Context, paneID string) (multiplexer.Pane, error) {
				return multiplexer.Pane{PaneID: paneID}, nil
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
		_, err := c.PaneRead(context.Background(), other, multiplexer.PaneReadOptions{})
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

	t.Run("PaneRead: 所有 pane は inner へ委譲する", func(t *testing.T) {
		fake := newFake()
		c := newScopedClient(fake, owned)
		if _, err := c.PaneRead(context.Background(), owned, multiplexer.PaneReadOptions{}); err != nil {
			t.Fatalf("PaneRead(owned) error = %v", err)
		}
		if !containsCall(fake.Calls, "PaneRead:"+owned) {
			t.Errorf("inner.PaneRead が呼ばれていない: %v", fake.Calls)
		}
	})
}

func TestSimulateDryRunClient_送信系はinnerを呼ばずログだけ出す(t *testing.T) {
	fake := &multiplexerfake.Client{}
	var buf bytes.Buffer
	c := simulateDryRunClient{inner: fake, w: &buf}

	if err := c.SendText(context.Background(), "p1", "Continue where you left off."); err != nil {
		t.Fatalf("SendText() error = %v", err)
	}
	if err := c.SendKeys(context.Background(), "p1", "esc", "enter"); err != nil {
		t.Fatalf("SendKeys() error = %v", err)
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
	fake := &multiplexerfake.Client{}
	c := simulateDryRunClient{inner: fake, w: &bytes.Buffer{}}

	if _, err := c.PaneRead(context.Background(), "p1", multiplexer.PaneReadOptions{}); err != nil {
		t.Fatalf("PaneRead() error = %v", err)
	}
	if !containsCall(fake.Calls, "PaneRead:p1") {
		t.Errorf("inner.PaneRead が呼ばれていない: %v", fake.Calls)
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

// realRexSimulateEnv を 1 にしたときだけ TestRunSimulate_RealRex が動く。rex の block の中で
// `go test ./...` を打つのは日常の開発ループで、そのたびに利用者の session へ block を割って
// 打ち込むのは避けたい。
const realRexSimulateEnv = "AGENTS_DAEMON_REAL_REX_SIMULATE"

// TestRunSimulate_RealRex は実 rex に対する統合テスト。rex の block 内 (REX_SESSION と REX_BLOCK
// あり) で realRexSimulateEnv=1 を付けて打つ。
//
// これは「simulate が本当に実 pane に対して検知〜送信の経路を通せる」ことを保証する
// 最終防衛線。実 Claude セッションへ触れないことはここでは確認しない。それは
// scopedClient のユニットテスト (所有 pane 以外への操作を全て拒否する) が
// 型と経路のレベルで保証しており、実行結果の観察に頼るより確実なため。
func TestRunSimulate_RealRex(t *testing.T) {
	if os.Getenv(realRexSimulateEnv) != "1" {
		t.Skipf("%s=1 が無いので skip する", realRexSimulateEnv)
	}
	if os.Getenv("REX_SESSION") == "" || os.Getenv("REX_BLOCK") == "" {
		t.Skip("rex の block 内 (REX_SESSION と REX_BLOCK あり) でないので skip する")
	}
	if _, err := exec.LookPath("rex"); err != nil {
		t.Skip("rex が PATH に無いので skip する")
	}

	home := t.TempDir()
	var buf bytes.Buffer
	if err := Run(context.Background(), &buf, apppath.New(home, "", ""), Options{}); err != nil {
		t.Fatalf("Run() error = %v\n出力:\n%s", err, buf.String())
	}
	out := buf.String()
	t.Logf("simulate 出力:\n%s", out)

	for _, want := range []string{
		"(backend=rex)",
		"を作成しました",
		"Claude agent として見えました (agent=claude agent_status=idle)",
		"limit 風の画面が表示されました",
		"1 回目の Tick → Outcome: waiting",
		"2 回目の Tick → Outcome:",
		"を閉じました",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("出力に %q が含まれていない", want)
		}
	}
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
