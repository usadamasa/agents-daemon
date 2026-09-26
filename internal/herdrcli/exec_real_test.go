package herdrcli

import (
	"context"
	"os/exec"
	"testing"
	"time"
)

// TestExecClient_RealHerdr_PaneList は実 herdr バイナリに対する統合テスト｡
// herdr が PATH に無い､または server が起動していないマシンでは skip する
// (このリポジトリの CI・他の開発機を壊さないため)｡
func TestExecClient_RealHerdr_PaneList(t *testing.T) {
	if _, err := exec.LookPath("herdr"); err != nil {
		t.Skip("herdr が PATH に無いので skip する")
	}

	c := newExecClientWithTimeout(5 * time.Second)
	if err := c.Reachable(context.Background()); err != nil {
		t.Skipf("herdr server に到達できないので skip する: %v", err)
	}

	panes, err := c.PaneList(context.Background())
	if err != nil {
		t.Fatalf("PaneList() error = %v", err)
	}
	t.Logf("実機の pane 数: %d", len(panes))
	for _, p := range panes {
		if p.PaneID == "" {
			t.Errorf("pane_id が空のエントリがある: %+v", p)
		}
	}
}
