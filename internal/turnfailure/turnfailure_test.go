package turnfailure

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, 11, 1, 14, 0, 0, 0, time.UTC)

// cc-premise: 2.1.316 hooks StopFailure の入力 (docs の例と、バイナリの組み立て)
const rateLimitInput = `{"session_id": "s1", "transcript_path": "/x.jsonl", "cwd": "/w", "hook_event_name": "StopFailure",` +
	` "error": "rate_limit", "error_details": "429 Too Many Requests", "last_assistant_message": "API Error: Rate limit reached"}`

func TestIngest_writesAndLoads(t *testing.T) {
	s := New(t.TempDir())
	if err := s.Ingest([]byte(rateLimitInput), now); err != nil {
		t.Fatalf("Ingest() error = %v", err)
	}
	got, err := s.Load("s1")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	want := Record{
		SessionID: "s1", Error: "rate_limit", ErrorDetails: "429 Too Many Requests",
		Message: "API Error: Rate limit reached", ObservedAt: now,
	}
	if got == nil || !got.ObservedAt.Equal(want.ObservedAt) {
		t.Fatalf("Load() = %+v, want %+v", got, want)
	}
	got.ObservedAt = want.ObservedAt
	if *got != want {
		t.Errorf("Load() = %+v, want %+v", *got, want)
	}
}

// 同じセッションの次の失敗で上書きする (最後の失敗だけが要る)。
func TestIngest_overwritesSameSession(t *testing.T) {
	s := New(t.TempDir())
	if err := s.Ingest([]byte(rateLimitInput), now); err != nil {
		t.Fatal(err)
	}
	later := now.Add(time.Minute)
	if err := s.Ingest([]byte(`{"session_id": "s1", "hook_event_name": "StopFailure", "error": "overloaded"}`), later); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load("s1")
	if err != nil || got == nil {
		t.Fatalf("Load() = %v, %v", got, err)
	}
	if got.Error != "overloaded" || got.ErrorDetails != "" || !got.ObservedAt.Equal(later) {
		t.Errorf("Load() = %+v, want overloaded at %v", got, later)
	}
}

// error が無い入力は Claude Code の扱いに合わせて unknown として残す。
func TestIngest_missingErrorIsUnknown(t *testing.T) {
	s := New(t.TempDir())
	if err := s.Ingest([]byte(`{"session_id": "s1", "hook_event_name": "StopFailure"}`), now); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load("s1")
	if err != nil || got == nil || got.Error != "unknown" {
		t.Errorf("Load() = %+v, %v, want error=unknown", got, err)
	}
}

func TestIngest_rejects(t *testing.T) {
	tests := map[string]string{
		"壊れた JSON":          `{"session_id": `,
		"別の hook の入力":       `{"session_id": "s1", "hook_event_name": "Stop"}`,
		"ファイル名に使えない session": `{"session_id": "../x", "hook_event_name": "StopFailure", "error": "rate_limit"}`,
	}
	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if err := New(dir).Ingest([]byte(input), now); err == nil {
				t.Error("Ingest() error = nil, want error")
			}
			if entries, _ := os.ReadDir(filepath.Join(dir, "turn-failures")); len(entries) != 0 {
				t.Errorf("書かれたファイル = %v, want なし", entries)
			}
		})
	}
}

// session_id が無ければ判断材料が無いので何も書かず、エラーにもしない。
func TestIngest_noSessionID(t *testing.T) {
	dir := t.TempDir()
	if err := New(dir).Ingest([]byte(`{"hook_event_name": "StopFailure", "error": "rate_limit"}`), now); err != nil {
		t.Fatalf("Ingest() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "turn-failures")); !os.IsNotExist(err) {
		t.Errorf("turn-failures が作られた: %v", err)
	}
}

func TestLoad_absent(t *testing.T) {
	s := New(t.TempDir())
	for _, id := range []string{"", "missing"} {
		got, err := s.Load(id)
		if got != nil || err != nil {
			t.Errorf("Load(%q) = %v, %v, want nil, nil", id, got, err)
		}
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
	if err := s.Ingest([]byte(rateLimitInput), now); err != nil {
		t.Fatal(err)
	}
	old := now.Add(-Retention - time.Hour)
	if err := os.Chtimes(filepath.Join(s.Dir, "s1.json"), old, old); err != nil {
		t.Fatal(err)
	}
	n, err := s.Prune(now)
	if err != nil || n != 1 {
		t.Errorf("Prune() = %d, %v, want 1, nil", n, err)
	}
	if n, err := (Store{}).Prune(now); err != nil || n != 0 {
		t.Errorf("ゼロ値の Prune() = %d, %v, want 0, nil", n, err)
	}
}

// ゼロ値の Store (state を持たない daemon のテスト) は読みで「無い」を返す。
func TestLoad_zeroStore(t *testing.T) {
	if got, err := (Store{}).Load("s1"); got != nil || err != nil {
		t.Errorf("ゼロ値の Load() = %v, %v, want nil, nil", got, err)
	}
}
