package rexcli

import (
	"context"
	"strings"
	"testing"
)

// SessionBlocks は session の block を flavor を問わず数える (shell 以外の block が残る session も閉じない)。
// session が無ければ found=false で、error にしない。
func TestExecClient_SessionBlocks(t *testing.T) {
	tests := []struct {
		name      string
		fixture   string // block ls の応答。空なら置かない (rex が exit 3)
		fail      bool
		wantN     int
		wantFound bool
		wantErr   bool
	}{
		{name: "空", fixture: `{"blocks":[]}`, wantN: 0, wantFound: true},
		{name: "shell 以外も数える", fixture: `{"blocks":[{"block_id":"block:a","flavor":"com.superlogical.terminal.shell"},{"block_id":"block:b","flavor":"com.superlogical.browser"}]}`, wantN: 2, wantFound: true},
		{name: "session が無い", wantFound: false},
		{name: "rex が失敗した", fixture: `{"blocks":[]}`, fail: true, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeRex(t)
			if tt.fixture != "" {
				f.put(t, "block-ls-session:x.json", tt.fixture)
			}
			if tt.fail {
				f.put(t, "fail-block-ls-session:x.json", "")
			}
			n, found, err := f.client(staticResolver(nil)).SessionBlocks(context.Background(), "session:x")
			if (err != nil) != tt.wantErr {
				t.Fatalf("SessionBlocks() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if n != tt.wantN || found != tt.wantFound {
				t.Errorf("SessionBlocks() = %d, %v, want %d, %v", n, found, tt.wantN, tt.wantFound)
			}
			f.assertCalls(t, "--autostart=false block ls -s session:x --json")
		})
	}
}

// SessionKill は session を ID で閉じる。
func TestExecClient_SessionKill(t *testing.T) {
	f := newFakeRex(t)
	if err := f.client(staticResolver(nil)).SessionKill(context.Background(), "session:x"); err != nil {
		t.Fatalf("SessionKill() error = %v", err)
	}
	f.assertCalls(t, "--autostart=false session kill session:x")
}

// 空の ID では rex を叩かない (rex は空の target を選択中の session と読みうる)。
func TestExecClient_SessionKill_Empty(t *testing.T) {
	f := newFakeRex(t)
	err := f.client(staticResolver(nil)).SessionKill(context.Background(), "")
	if err == nil || !strings.Contains(err.Error(), "空") {
		t.Errorf("SessionKill(\"\") error = %v, want 空の ID の error", err)
	}
	f.assertCalls(t)
}
