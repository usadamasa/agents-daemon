package parking

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/usadamasa/agents-daemon/internal/worklog"
)

func writeWork(t *testing.T, stateDir, sessionID, cwd, prompt string) worklog.Store {
	t.Helper()
	s := worklog.New(stateDir)
	in := `{"session_id": "` + sessionID + `", "cwd": "` + cwd + `", "hook_event_name": "UserPromptSubmit", "prompt": "` + prompt + `"}`
	if _, err := s.Ingest([]byte(in), testNow.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	in = `{"session_id": "` + sessionID + `", "hook_event_name": "Stop", "last_assistant_message": "表の列を直した"}`
	if _, err := s.Ingest([]byte(in), testNow.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	return s
}

// 終了したセッションの作業は、見せる題を title、最後の要約を detail にした kind scope、出自 adopted のメモにしてから記録を消す。
func TestAdoptWork_movesWorkToParking(t *testing.T) {
	stateDir := t.TempDir()
	cwd := t.TempDir()
	works := writeWork(t, stateDir, "dead", cwd, "README を直して")
	if _, err := works.Set("dead", worklog.Change{Inferred: new("README の表の修正")}, testNow.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	s := New(stateDir)

	added, err := s.AdoptWork("dead", works, exited, testNow)
	if err != nil {
		t.Fatalf("AdoptWork() error = %v", err)
	}
	item := added.Item
	if !added.Created || item.Kind != KindScope || item.Origin != OriginAdopted || item.Title != "README の表の修正" || item.Detail != "表の列を直した" || item.Repo != cwd {
		t.Errorf("item = %+v", item)
	}
	if len(item.Sources) != 1 || item.Sources[0].SessionID != "dead" || item.Sources[0].TaskID != "" || item.Sources[0].CWD != cwd {
		t.Errorf("sources = %+v", item.Sources)
	}
	if _, err := os.Stat(filepath.Join(stateDir, "work", "dead.json")); !os.IsNotExist(err) {
		t.Errorf("作業の記録が残った: %v", err)
	}
}

// 記録が無い、題が無い (prompt を打っていない)、終了を確かめられない作業は移さない。確かめる側には記録の updated_at を渡す。
func TestAdoptWork_rejects(t *testing.T) {
	stateDir := t.TempDir()
	works := worklog.New(stateDir)
	s := New(stateDir)
	if _, err := s.AdoptWork("none", works, exited, testNow); err == nil {
		t.Error("記録の無い AdoptWork() が通った")
	}
	if _, err := works.Ingest([]byte(`{"session_id": "quiet", "cwd": "/w", "hook_event_name": "Stop", "last_assistant_message": "x"}`), testNow); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AdoptWork("quiet", works, exited, testNow); err == nil || !strings.Contains(err.Error(), "題が無い") {
		t.Errorf("題の無い AdoptWork() = %v", err)
	}

	writeWork(t, stateDir, "alive", t.TempDir(), "まだ動いている")
	var askedAfter time.Time
	alive := func(_ string, after time.Time) error { askedAfter = after; return errors.New("セッション alive は生きている") }
	if _, err := s.AdoptWork("alive", works, alive, testNow); err == nil || !strings.Contains(err.Error(), "生きている") {
		t.Errorf("AdoptWork(alive) = %v", err)
	}
	if !askedAfter.Equal(testNow.Add(-time.Hour)) {
		t.Errorf("確かめた時刻 = %v, want 記録の updated_at", askedAfter)
	}
	if _, ok := works.Get("alive"); !ok {
		t.Error("生きているセッションの記録が消えた")
	}
	if got, _ := s.list(); len(got) != 0 {
		t.Errorf("メモができた: %+v", got)
	}
}
