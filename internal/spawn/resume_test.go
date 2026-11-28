package spawn

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/usadamasa/agents-daemon/internal/multiplexer"
)

const desktopSID = "8cb3bbba-74de-4582-b9e3-9c94f6fe2843"

// 成功: cwd を cwd にした新しい多重化ツールの session で `claude --resume <id>` を起こし、claude が前景になってから
// 開いた session ID と pane を返す。新しい session ID は作らない (同じ会話を続けるため)。
func TestRunner_Resume(t *testing.T) {
	host := &fakeHost{}
	r := runner(host)
	r.NewSessionID = func() (string, error) { t.Fatal("resume で新しい session ID を作った"); return "", nil }
	got, err := r.Resume(context.Background(), ResumeRequest{SessionID: desktopSID, Cwd: repoHome})
	if err != nil {
		t.Fatalf("Resume() error = %v", err)
	}
	want := Result{SessionID: desktopSID, PaneID: paneID, RexSessionID: createdRexSID, WindowID: windowID, Foreground: true}
	if got != want {
		t.Errorf("Resume() = %+v, want %+v", got, want)
	}
	wantRuns := []runCall{{label: "example-repo", cwd: repoHome, argv: []string{"claude", "--resume", desktopSID}}}
	if !reflect.DeepEqual(host.runs, wantRuns) {
		t.Errorf("NewSession の引数 = %+v, want %+v", host.runs, wantRuns)
	}
}

// claude が前景にならなければ session を残したまま Foreground=false と理由を返す (Run と同じ)。
func TestRunner_Resume_notForeground(t *testing.T) {
	host := &fakeHost{paneGet: func(id string) (multiplexer.Pane, error) {
		return multiplexer.Pane{PaneID: id, Agent: "zsh"}, nil
	}}
	got, err := runner(host).Resume(context.Background(), ResumeRequest{SessionID: desktopSID, Cwd: repoHome})
	if err != nil {
		t.Fatalf("Resume() error = %v", err)
	}
	if got.Foreground || !strings.Contains(got.Detail, paneID) {
		t.Errorf("Resume() = %+v, want Foreground=false と pane を含む Detail", got)
	}
}

// 入力が足りなければ session を作らない。
func TestRunner_Resume_rejects(t *testing.T) {
	cases := []struct {
		name    string
		req     ResumeRequest
		wantErr string
	}{
		{"session ID が空", ResumeRequest{Cwd: repoHome}, "session ID が空"},
		{"session ID に使えない文字", ResumeRequest{SessionID: "../x", Cwd: repoHome}, "使えない文字"},
		{"cwd が相対", ResumeRequest{SessionID: desktopSID, Cwd: "src"}, "絶対パス"},
		{"cwd が無い", ResumeRequest{SessionID: desktopSID, Cwd: "/nowhere"}, "見つからない"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			host := &fakeHost{}
			_, err := runner(host).Resume(context.Background(), tc.req)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Resume() error = %v, want %q を含む", err, tc.wantErr)
			}
			if len(host.runs) != 0 {
				t.Errorf("session を作った: %+v", host.runs)
			}
		})
	}
}

// session を作れなければ error。
func TestRunner_Resume_newSessionFails(t *testing.T) {
	host := &fakeHost{fail: errors.New("rex down")}
	if _, err := runner(host).Resume(context.Background(), ResumeRequest{SessionID: desktopSID, Cwd: repoHome}); err == nil || !strings.Contains(err.Error(), "rex down") {
		t.Fatalf("Resume() error = %v, want rex down", err)
	}
}
