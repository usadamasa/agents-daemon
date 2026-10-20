package limitlog

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/usadamasa/agents-daemon/internal/multiplexer"
)

func TestAppend(t *testing.T) {
	s := New(t.TempDir())
	at := time.Date(2026, 10, 20, 23, 5, 0, 0, time.FixedZone("JST", 9*3600))
	ev := Event{
		At:        at,
		PaneID:    "block:1",
		SessionID: "sid-1",
		CWD:       "/w",
		Outcome:   "waiting",
		Kind:      "limit",
		Attempts:  0,
		Record:    Record{AgentStatus: "working", Msg: "長い作業"},
		Geometry:  &multiplexer.Geometry{Rows: 11, Columns: 139, Screen: multiplexer.ScreenAlternate},
		RateLimit: &Window{UsedPercentage: 100, ResetsAt: at.Add(time.Hour), ObservedAt: at.Add(-time.Minute), SessionID: "sid-1"},
		Settings:  Settings{ReadLines: 25, DetectionTailLines: 15},
		Screen:    "line 1\nline 2\n",
	}
	if err := s.Append(ev); err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	ev.Outcome = "retried"
	ev.Attempts = 1
	if err := s.Append(ev); err != nil {
		t.Fatalf("Append() 2 回目 error = %v", err)
	}
	ev.At = at.Add(24 * time.Hour)
	if err := s.Append(ev); err != nil {
		t.Fatalf("Append() 翌日 error = %v", err)
	}

	// 1 出来事 1 行で、日付ごとのファイルに入る。
	data, err := os.ReadFile(filepath.Join(s.Dir, "2026-10-20.jsonl"))
	if err != nil {
		t.Fatalf("当日のファイルが無い: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("行数 = %d, want 2:\n%s", len(lines), data)
	}
	var got Event
	if err := json.Unmarshal([]byte(lines[0]), &got); err != nil {
		t.Fatalf("1 行目を JSON として読めない: %v", err)
	}
	if got.Outcome != "waiting" || got.Kind != "limit" || got.Screen != "line 1\nline 2\n" || got.Geometry == nil || got.Geometry.Rows != 11 {
		t.Errorf("読み戻した Event = %+v", got)
	}
	if got.RateLimit == nil || !got.RateLimit.ResetsAt.Equal(at.Add(time.Hour)) {
		t.Errorf("rate_limit が往復していない: %+v", got.RateLimit)
	}
	if !strings.Contains(lines[1], `"attempts":1`) {
		t.Errorf("2 行目に attempts が無い: %s", lines[1])
	}
	if _, err := os.Stat(filepath.Join(s.Dir, "2026-10-21.jsonl")); err != nil {
		t.Errorf("翌日のファイルが無い: %v", err)
	}
	// 無いものは書かない (後から読む人が「欄が無い = 取れなかった」と読めるように)。
	if strings.Contains(lines[0], `"account"`) || strings.Contains(lines[0], `"geometry_error"`) {
		t.Errorf("無い欄が出ている: %s", lines[0])
	}
}

// ゼロ値の Store は書かない (daemon のテストが置き場を用意せずに tick を回せるようにするため)。
func TestAppend_ZeroStore(t *testing.T) {
	if err := (Store{}).Append(Event{At: time.Now()}); err != nil {
		t.Fatalf("ゼロ値の Store の Append() error = %v", err)
	}
}

func TestPrune(t *testing.T) {
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	s := New(t.TempDir())
	if err := s.Append(Event{At: now}); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(s.Dir, "2026-09-01.jsonl")
	if err := os.WriteFile(old, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	mtime := now.Add(-Retention - time.Hour)
	if err := os.Chtimes(old, mtime, mtime); err != nil {
		t.Fatal(err)
	}

	removed, err := s.Prune(now)
	if err != nil || removed != 1 {
		t.Fatalf("Prune() = %d, %v, want 1", removed, err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Errorf("保持期間を過ぎたファイルが残っている (err=%v)", err)
	}
	if _, err := os.Stat(filepath.Join(s.Dir, "2026-10-20.jsonl")); err != nil {
		t.Errorf("当日のファイルが消えた: %v", err)
	}

	// ディレクトリが無い、ゼロ値の Store は何もしない。
	if n, err := New(filepath.Join(t.TempDir(), "none")).Prune(now); err != nil || n != 0 {
		t.Errorf("無いディレクトリの Prune() = %d, %v", n, err)
	}
	if n, err := (Store{}).Prune(now); err != nil || n != 0 {
		t.Errorf("ゼロ値の Prune() = %d, %v", n, err)
	}
}
