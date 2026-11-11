package reaper

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/usadamasa/agents-daemon/internal/applog"
	"github.com/usadamasa/agents-daemon/internal/spawnedsession"
)

// fakeClient は blocks に session → block の数を持つ (無ければ not found)。
type fakeClient struct {
	blocks   map[string]int
	countErr error
	killErr  error
	killed   []string
	counted  []string
}

func (c *fakeClient) SessionBlocks(_ context.Context, sessionID string) (int, bool, error) {
	c.counted = append(c.counted, sessionID)
	if c.countErr != nil {
		return 0, false, c.countErr
	}
	n, ok := c.blocks[sessionID]
	return n, ok, nil
}

func (c *fakeClient) SessionKill(_ context.Context, sessionID string) error {
	c.killed = append(c.killed, sessionID)
	return c.killErr
}

func markedIDs(t *testing.T, s spawnedsession.Store) []string {
	t.Helper()
	recs, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, r := range recs {
		out = append(out, r.SessionID)
	}
	slices.Sort(out)
	return out
}

func newReaper(t *testing.T, client Client, marked ...string) (*Reaper, spawnedsession.Store) {
	t.Helper()
	return newReaperDry(t, client, false, marked...)
}

func newReaperDry(t *testing.T, client Client, dryRun bool, marked ...string) (*Reaper, spawnedsession.Store) {
	t.Helper()
	dir := t.TempDir()
	marks := spawnedsession.New(dir)
	for _, id := range marked {
		if err := marks.Put(id); err != nil {
			t.Fatal(err)
		}
	}
	log := applog.NewLogger(filepath.Join(dir, "daemon.log"), time.Now)
	t.Cleanup(func() { _ = log.Close() })
	return New(marks, client, log, dryRun), marks
}

// 印のある session は、block が無くなっていれば閉じて印を消す。block が残っていれば閉じずに印を残し、
// session がもう無ければ印だけ消す。数えられなかった、閉じられなかったときは印を残して次に回す (#557)。
func TestReap(t *testing.T) {
	const (
		empty = "session:empty"
		busy  = "session:busy"
		gone  = "session:gone"
	)
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name       string
		client     *fakeClient
		wantKilled []string
		wantMarked []string
	}{
		{
			name:       "空なら閉じ、残っていれば残し、無ければ印だけ消す",
			client:     &fakeClient{blocks: map[string]int{empty: 0, busy: 2}},
			wantKilled: []string{empty},
			wantMarked: []string{busy},
		},
		{
			name:       "数えられなければ何もしない",
			client:     &fakeClient{countErr: errors.New("rex: server error")},
			wantMarked: []string{busy, empty, gone},
		},
		{
			name:       "閉じられなければ印を残す",
			client:     &fakeClient{blocks: map[string]int{empty: 0, busy: 1, gone: 1}, killErr: errors.New("rex: server error")},
			wantKilled: []string{empty},
			wantMarked: []string{busy, empty, gone},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, marks := newReaper(t, tt.client, empty, busy, gone)
			p.Reap(context.Background(), now)
			if !slices.Equal(tt.client.killed, tt.wantKilled) {
				t.Errorf("閉じた session = %q, want %q", tt.client.killed, tt.wantKilled)
			}
			if got := markedIDs(t, marks); !slices.Equal(got, tt.wantMarked) {
				t.Errorf("残った印 = %q, want %q", got, tt.wantMarked)
			}
		})
	}
}

// dry-run は数えるが閉じない。閉じていない session の印は残す (dry-run を外した daemon が閉じる)。
func TestReap_DryRun(t *testing.T) {
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	client := &fakeClient{blocks: map[string]int{"session:empty": 0}}
	p, marks := newReaperDry(t, client, true, "session:empty")
	p.Reap(context.Background(), now)
	if len(client.killed) != 0 || len(client.counted) != 1 {
		t.Errorf("閉じた = %q、数えた = %q, want 閉じずに 1 回数える", client.killed, client.counted)
	}
	if got := markedIDs(t, marks); !slices.Equal(got, []string{"session:empty"}) {
		t.Errorf("残った印 = %q, want [session:empty]", got)
	}
}

// 印を回るのは Interval ごと。tick ごとに多重化ツールを叩かない。nil の Reaper は何もしない。
func TestReap_Interval(t *testing.T) {
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	client := &fakeClient{blocks: map[string]int{"session:busy": 1}}
	p, _ := newReaper(t, client, "session:busy")
	for _, at := range []time.Time{now, now.Add(Interval / 2), now.Add(Interval)} {
		p.Reap(context.Background(), at)
	}
	if len(client.counted) != 2 {
		t.Errorf("数えた回数 = %d, want 2 (間隔の内側では回らない)", len(client.counted))
	}
	var nilReaper *Reaper
	nilReaper.Reap(context.Background(), now)
}
