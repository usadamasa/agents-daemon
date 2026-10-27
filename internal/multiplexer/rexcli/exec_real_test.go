package rexcli

import (
	"context"
	"os/exec"
	"slices"
	"testing"
	"time"

	"github.com/usadamasa/agents-daemon/internal/multiplexer"
)

// TestExecClient_RealRex_PaneList は実 rex に対する読み取りだけの統合テスト。
// rex が PATH に無い、または server が起動していないマシンでは skip する。
// --autostart=false が付くので、落ちている server を起こすことは無い。
func TestExecClient_RealRex_PaneList(t *testing.T) {
	if _, err := exec.LookPath("rex"); err != nil {
		t.Skip("rex が PATH に無いので skip する")
	}

	// 実際の state dir を読まないよう、対応は空で渡す。
	c := newExecClientWithTimeout(func() (map[string]string, error) { return nil, nil }, 5*time.Second)
	if err := c.Reachable(context.Background()); err != nil {
		t.Skipf("rex server に到達できないので skip する: %v", err)
	}

	panes, err := c.PaneList(context.Background())
	if err != nil {
		t.Fatalf("PaneList() error = %v", err)
	}
	t.Logf("実機の block 数: %d", len(panes))
	for _, p := range panes {
		if p.PaneID == "" {
			t.Errorf("block_id が空のエントリがある: %+v", p)
		}
	}
}

// TestExecClient_RealRex_WatchMatchesListing は実 rex に購読を張り、同期した cache の一覧と全列挙の一覧で
// block の集合が同じかを見る。状態 (working / done) は 2 回の取得の間に変わりうるので比べない。
func TestExecClient_RealRex_WatchMatchesListing(t *testing.T) {
	if _, err := exec.LookPath("rex"); err != nil {
		t.Skip("rex が PATH に無いので skip する")
	}
	noSessions := func() (map[string]string, error) { return nil, nil }
	listing := newExecClientWithTimeout(noSessions, 5*time.Second)
	if err := listing.Reachable(context.Background()); err != nil {
		t.Skipf("rex server に到達できないので skip する: %v", err)
	}

	watching := newExecClientWithTimeout(noSessions, 5*time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	watching.Watch(ctx, t.Logf)
	deadline := time.Now().Add(15 * time.Second)
	for !watching.watch.synced() {
		if time.Now().After(deadline) {
			t.Fatal("15 秒で購読が同期しない")
		}
		time.Sleep(50 * time.Millisecond)
	}

	cached, err := watching.PaneList(context.Background())
	if err != nil {
		t.Fatalf("cache の PaneList() error = %v", err)
	}
	listed, err := listing.PaneList(context.Background())
	if err != nil {
		t.Fatalf("全列挙の PaneList() error = %v", err)
	}
	ids := func(panes []multiplexer.Pane) []string {
		out := make([]string, 0, len(panes))
		for _, p := range panes {
			out = append(out, p.PaneID)
		}
		return out
	}
	if got, want := ids(cached), ids(listed); !slices.Equal(got, want) {
		t.Errorf("cache の block = %v, 全列挙の block = %v", got, want)
	}
	t.Logf("cache と全列挙の block 数: %d", len(cached))
}
