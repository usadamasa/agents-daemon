package rexcli

import (
	"github.com/usadamasa/agents-daemon/internal/multiplexer"
)

// blockState は shell の block 1 つ分の cache。
type blockState struct {
	session string
	ps      programStatusResult
	proc    processResult
}

// blockRef は block_created が埋めるよう求めた block。
type blockRef struct {
	session string
	block   string
}

// paneCache は events の stream を当てて保つ shell の block の状態。
type paneCache struct {
	blocks map[string]*blockState
}

func newPaneCache(blocks map[string]*blockState) *paneCache {
	return &paneCache{blocks: blocks}
}

func (c *paneCache) apply(line []byte) (blockRef, error) {
	return blockRef{}, nil
}

func (c *paneCache) put(ref blockRef, st *blockState) {}

func (c *paneCache) panes(sessions map[string]string) []multiplexer.Pane {
	return nil
}
