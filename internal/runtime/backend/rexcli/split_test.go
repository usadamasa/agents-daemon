package rexcli

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/usadamasa/agents-daemon/internal/runtime/backend/rexcli/blockstate"
)

// newSplitJSON は session.new_split の応答。形は 2026-11-17 の実測。
const newSplitJSON = `{"anchor_id":"` + blockClaudeWorking + `","block_ids":["block:01a1-new"],"split_ids":["split:01a1"],"revision":4}`

// SplitPane は anchor の block の下に block を作り、shell を挟まず argv を直に起こす (simulate の使い捨て pane)。
// CLI の split --split=below --ratio 40 --focus=false --keep-open --shell=none と同じ形にする:
// ratio は上 (anchor) の取り分で 0.6、login を切り (切らないと /usr/bin/login を挟んで exit code が 0 に化ける)、
// 終わっても block を残す。作った block は CLI と同じく作った client の持ち物にしない。
func TestExecClient_SplitPane(t *testing.T) {
	f := newFakeAPI(t)
	f.addSession(sessionA, [2]string{blockClaudeWorking, blockstate.ShellFlavor})
	f.srv.Reply("session.new_split", json.RawMessage(newSplitJSON))
	argv := []string{"/bin/sh", "/tmp/sim/simulate.sh", "/tmp/sim/screen.txt"}

	got, err := f.client(staticResolver(nil)).SplitPane(context.Background(), blockClaudeWorking, "agents-daemon-simulate", argv)
	if err != nil {
		t.Fatalf("SplitPane() error = %v", err)
	}
	if got != "block:01a1-new" {
		t.Errorf("SplitPane() = %q, want block:01a1-new", got)
	}
	f.assertCalls(t,
		"target.resolve "+blockClaudeWorking,
		"session.new_split "+sessionA,
		"session.attach "+sessionA,
		"session.disown_block "+sessionA,
	)
	var req struct {
		SessionID string  `json:"session_id"`
		AnchorID  string  `json:"anchor_id"`
		Direction string  `json:"direction"`
		Side      string  `json:"side"`
		Ratio     float64 `json:"ratio"`
		Focus     *bool   `json:"focus"`
		Layout    struct {
			Block blockOptions `json:"block"`
		} `json:"layout"`
	}
	f.payloadOf(t, "session.new_split", &req)
	if req.AnchorID != blockClaudeWorking || req.Direction != "vertical" || req.Side != "after" || req.Ratio != 0.6 || req.Focus == nil || *req.Focus {
		t.Errorf("new_split = %+v", req)
	}
	o := req.Layout.Block.Options
	if req.Layout.Block.Label != "agents-daemon-simulate" || !slices.Equal(o.Command, argv) ||
		o.Shell == nil || *o.Shell != "none" || o.Login == nil || *o.Login || o.Exit == nil || o.Exit.OnCompletion {
		t.Errorf("block = %+v", req.Layout.Block)
	}
}

func TestExecClient_SplitPane_EmptyBlockIDs(t *testing.T) {
	f := newFakeAPI(t)
	f.addSession(sessionA, [2]string{blockClaudeWorking, blockstate.ShellFlavor})
	f.srv.Reply("session.new_split", json.RawMessage(`{"anchor_id":"`+blockClaudeWorking+`","block_ids":[],"split_ids":[],"revision":4}`))
	_, err := f.client(staticResolver(nil)).SplitPane(context.Background(), blockClaudeWorking, "x", []string{"/bin/sh"})
	if err == nil || !strings.Contains(err.Error(), "block_ids") {
		t.Fatalf("SplitPane() error = %v, want block_ids が空の error", err)
	}
}

// ClosePane は block を block.close で閉じる。無い block は not found の error になる。
func TestExecClient_ClosePane(t *testing.T) {
	f := newFakeAPI(t)
	f.addSession(sessionA, [2]string{blockClaudeWorking, blockstate.ShellFlavor})
	c := f.client(staticResolver(nil))
	if err := c.ClosePane(context.Background(), blockClaudeWorking); err != nil {
		t.Fatalf("ClosePane() error = %v", err)
	}
	f.assertCalls(t, "target.resolve "+blockClaudeWorking, "block.close "+blockClaudeWorking)
	if err := c.ClosePane(context.Background(), blockClosed); !isNotFound(err) {
		t.Errorf("無い block の ClosePane() error = %v, want not found", err)
	}
}
