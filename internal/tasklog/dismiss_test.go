package tasklog

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// 終了したセッションのタスクは、完了しているかを問わず記録を消す。exited にはタスクの記録の時刻を渡す。
func TestDismiss_removesTaskOfExitedSession(t *testing.T) {
	for _, names := range [][]string{
		{"task-create-alpha"},
		{"task-create-alpha", "task-update-completed"},
	} {
		s := New(t.TempDir())
		ingest(t, s, names...)
		updated := byID(t, s)["1"].UpdatedAt
		var asked time.Time
		exited := func(sessionID string, after time.Time) error {
			if sessionID != fixtureSession {
				t.Errorf("exited の session = %q, want %q", sessionID, fixtureSession)
			}
			asked = after
			return nil
		}
		if err := s.Dismiss(fixtureSession, "1", exited); err != nil {
			t.Fatalf("%v: Dismiss() error = %v", names, err)
		}
		if !asked.Equal(updated) {
			t.Errorf("%v: exited の after = %v, want 記録の時刻 %v", names, asked, updated)
		}
		if _, ok := s.Get(fixtureSession, "1"); ok {
			t.Errorf("%v: 記録が残っている", names)
		}
	}
}

// セッションの終了を確かめられなければ記録を消さない (生きているセッションの記録を守る、LiveTaskKept)。
func TestDismiss_keepsTaskWhenSessionMayBeLive(t *testing.T) {
	s := New(t.TempDir())
	ingest(t, s, "task-create-alpha")
	err := s.Dismiss(fixtureSession, "1", func(string, time.Time) error { return errors.New("pane で生きている") })
	if err == nil || !strings.Contains(err.Error(), "pane で生きている") {
		t.Fatalf("Dismiss() error = %v, want 終了を確かめられない理由", err)
	}
	if _, ok := s.Get(fixtureSession, "1"); !ok {
		t.Error("終了を確かめられないのに記録を消した")
	}
}

// 記録の無いタスクと、ファイル名に使えない ID はエラーにし、exited を呼ばない。
func TestDismiss_rejectsMissingTask(t *testing.T) {
	s := New(t.TempDir())
	ingest(t, s, "task-create-alpha")
	for _, id := range []string{"9", "../1"} {
		err := s.Dismiss(fixtureSession, id, func(string, time.Time) error {
			t.Errorf("%q: 記録の無いタスクで exited を呼んだ", id)
			return nil
		})
		if err == nil {
			t.Errorf("Dismiss(%q) error = nil, want 記録が無い", id)
		}
	}
}
