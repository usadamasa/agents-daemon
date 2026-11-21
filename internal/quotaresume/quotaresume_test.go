package quotaresume

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/usadamasa/agents-daemon/internal/transcript"
)

var now = time.Date(2026, 11, 21, 14, 0, 0, 0, time.UTC)

// notification は Notification hook の入力。欄は docs の例に揃え、文面と title は docs に例が無いので仮の文にする。
// cc-premise: 2.1.330 hooks Notification の入力の欄 (message、title、notification_type) と quota_auto_resume_* の 3 種
func notification(sessionID, notificationType string) []byte {
	return []byte(`{"session_id": "` + sessionID + `", "transcript_path": "/x.jsonl", "cwd": "/w",` +
		` "hook_event_name": "Notification", "message": "usage limit notice", "notification_type": "` + notificationType + `"}`)
}

func TestIngest_writesAndLoads(t *testing.T) {
	s := New(t.TempDir())
	if err := s.Ingest(notification("s1", TypeStale), now); err != nil {
		t.Fatalf("Ingest() error = %v", err)
	}
	got, err := s.Load("s1")
	if err != nil || got == nil {
		t.Fatalf("Load() = %v, %v", got, err)
	}
	want := Record{SessionID: "s1", NotificationType: TypeStale, Message: "usage limit notice", ObservedAt: now}
	if !got.ObservedAt.Equal(want.ObservedAt) {
		t.Fatalf("ObservedAt = %v, want %v", got.ObservedAt, want.ObservedAt)
	}
	got.ObservedAt = want.ObservedAt
	if *got != want {
		t.Errorf("Load() = %+v, want %+v", *got, want)
	}
}

// 3 種のどれも最後の 1 件で上書きする。stale の後に fired か disabled が来たら、本体はもう Enter を待っていない。
func TestIngest_lastNoticeWins(t *testing.T) {
	for _, next := range []string{TypeFired, TypeDisabled} {
		t.Run(next, func(t *testing.T) {
			s := New(t.TempDir())
			if err := s.Ingest(notification("s1", TypeStale), now); err != nil {
				t.Fatal(err)
			}
			later := now.Add(time.Minute)
			if err := s.Ingest(notification("s1", next), later); err != nil {
				t.Fatal(err)
			}
			got, err := s.Load("s1")
			if err != nil || got == nil || got.NotificationType != next || !got.ObservedAt.Equal(later) {
				t.Errorf("Load() = %+v, %v, want %s at %v", got, err, next, later)
			}
		})
	}
}

// quota_auto_resume_* 以外の通知と Notification 以外の event は扱わない (hooks.json の matcher が絞る前提が崩れたら気づく)。
func TestIngest_rejects(t *testing.T) {
	tests := map[string][]byte{
		"壊れた JSON":           []byte(`{"session_id": `),
		"Notification 以外":    []byte(`{"session_id": "s1", "hook_event_name": "Stop"}`),
		"quota 以外の通知":        notification("s1", "permission_prompt"),
		"知らない quota の通知":     notification("s1", "quota_auto_resume_unknown"),
		"ファイル名に使えない session": notification("../x", TypeStale),
	}
	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if err := New(dir).Ingest(input, now); err == nil {
				t.Error("Ingest() error = nil, want error")
			}
			if entries, _ := os.ReadDir(filepath.Join(dir, "quota-resume")); len(entries) != 0 {
				t.Errorf("書かれたファイル = %v, want なし", entries)
			}
		})
	}
}

// session_id が無ければ何も書かず、エラーにもしない。
func TestIngest_noSessionID(t *testing.T) {
	dir := t.TempDir()
	input := []byte(`{"hook_event_name": "Notification", "notification_type": "quota_auto_resume_stale"}`)
	if err := New(dir).Ingest(input, now); err != nil {
		t.Fatalf("Ingest() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "quota-resume")); !os.IsNotExist(err) {
		t.Errorf("quota-resume が作られた: %v", err)
	}
}

func TestLoad_absent(t *testing.T) {
	s := New(t.TempDir())
	for _, id := range []string{"", "missing"} {
		if got, err := s.Load(id); got != nil || err != nil {
			t.Errorf("Load(%q) = %v, %v, want nil, nil", id, got, err)
		}
	}
	if got, err := (Store{}).Load("s1"); got != nil || err != nil {
		t.Errorf("ゼロ値の Load() = %v, %v, want nil, nil", got, err)
	}
}

func TestLoad_broken(t *testing.T) {
	s := New(t.TempDir())
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.Dir, "s1.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Load("s1"); err == nil || !strings.Contains(err.Error(), "s1.json") {
		t.Errorf("Load() error = %v, want パースの失敗", err)
	}
}

func TestPrune(t *testing.T) {
	s := New(t.TempDir())
	if err := s.Ingest(notification("s1", TypeStale), now); err != nil {
		t.Fatal(err)
	}
	old := now.Add(-Retention - time.Hour)
	if err := os.Chtimes(filepath.Join(s.Dir, "s1.json"), old, old); err != nil {
		t.Fatal(err)
	}
	if n, err := s.Prune(now); err != nil || n != 1 {
		t.Errorf("Prune() = %d, %v, want 1, nil", n, err)
	}
	if n, err := (Store{}).Prune(now); err != nil || n != 0 {
		t.Errorf("ゼロ値の Prune() = %d, %v, want 0, nil", n, err)
	}
}

// StaleFor は、今の上限の entry の後に届いた stale の通知だけを Enter の根拠にする。前の上限の残りや、
// fired / disabled の後の待機には使わない。
func TestRecord_StaleFor(t *testing.T) {
	limitAt := now.Add(-time.Hour)
	entry := &transcript.Entry{UUID: "u1", Type: "assistant", Error: "rate_limit", Timestamp: limitAt}
	tests := []struct {
		name  string
		rec   *Record
		entry *transcript.Entry
		want  bool
	}{
		{"entry の後の stale", &Record{NotificationType: TypeStale, ObservedAt: now}, entry, true},
		{"entry と同時刻の stale", &Record{NotificationType: TypeStale, ObservedAt: limitAt}, entry, true},
		{"entry より前の stale (前の上限の残り)", &Record{NotificationType: TypeStale, ObservedAt: limitAt.Add(-time.Second)}, entry, false},
		{"fired", &Record{NotificationType: TypeFired, ObservedAt: now}, entry, false},
		{"disabled", &Record{NotificationType: TypeDisabled, ObservedAt: now}, entry, false},
		{"記録が無い", nil, entry, false},
		{"entry が無い", &Record{NotificationType: TypeStale, ObservedAt: now}, nil, false},
		{"entry の時刻が読めない", &Record{NotificationType: TypeStale, ObservedAt: now}, &transcript.Entry{UUID: "u1"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.rec.StaleFor(tt.entry); got != tt.want {
				t.Errorf("StaleFor() = %v, want %v", got, tt.want)
			}
		})
	}
}
