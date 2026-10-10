package rexcli

import (
	"context"
	"os/exec"
	"testing"
	"time"
)

// TestExecClient_RealRex_PaneList は実 rex に対する読み取りだけの統合テスト｡
// rex が PATH に無い､または server が起動していないマシンでは skip する｡
// --autostart=false が付くので､落ちている server を起こすことは無い｡
func TestExecClient_RealRex_PaneList(t *testing.T) {
	if _, err := exec.LookPath("rex"); err != nil {
		t.Skip("rex が PATH に無いので skip する")
	}

	// 実際の state dir を読まないよう､対応は空で渡す｡
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
