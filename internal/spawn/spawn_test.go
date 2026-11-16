package spawn

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/usadamasa/agents-daemon/internal/multiplexer"
)

const (
	repoHome = "/src/home/example-repo"
	newSID   = "6b1f3c2a-1111-4222-8333-944455556666"
	paneID   = "block:01a15e6a-7fe3-7298-af51-8982a808f03b"
)

// fakeHost は Host のテスト用実装。run が nil なら session を作れないことにする。
type fakeHost struct {
	run     func(label, cwd string, argv []string) (string, error)
	paneGet func(paneID string) (multiplexer.Pane, error)
	// runs は NewSession と NewWindow に渡った引数。NewSession の呼び出しは session が空。
	runs []runCall
}

type runCall struct {
	session    string
	label, cwd string
	argv       []string
}

func (h *fakeHost) NewSession(_ context.Context, label, cwd string, argv []string) (string, error) {
	h.runs = append(h.runs, runCall{"", label, cwd, argv})
	if h.run == nil {
		return "", errors.New("fakeHost: run が未設定")
	}
	return h.run(label, cwd, argv)
}

func (h *fakeHost) NewWindow(_ context.Context, session, label, cwd string, argv []string) (string, error) {
	h.runs = append(h.runs, runCall{session, label, cwd, argv})
	if h.run == nil {
		return "", errors.New("fakeHost: run が未設定")
	}
	return h.run(label, cwd, argv)
}

func (h *fakeHost) PaneGet(_ context.Context, id string) (multiplexer.Pane, error) {
	if h.paneGet == nil {
		return multiplexer.Pane{}, errors.New("fakeHost: paneGet が未設定")
	}
	return h.paneGet(id)
}

func opened(string, string, []string) (string, error) { return paneID, nil }

func claudePane(id string) (multiplexer.Pane, error) {
	return multiplexer.Pane{PaneID: id, Agent: "claude", AgentStatus: multiplexer.AgentStatusUnknown}, nil
}

// exists は repoHome だけがディレクトリとして存在することにする。
func exists(path string) (os.FileInfo, error) {
	if path == repoHome {
		return os.Stat(".")
	}
	return nil, os.ErrNotExist
}

func runner(host *fakeHost) Runner {
	return Runner{
		Host:         host,
		NewSessionID: func() (string, error) { return newSID, nil },
		Stat:         exists,
		WaitTimeout:  50 * time.Millisecond,
		WaitInterval: time.Millisecond,
	}
}

func request() Request {
	return Request{Repo: repoHome, Prompt: "メモをタスクにしてください。\n詳細: a", Model: "claude-sonnet-5[1m]"}
}

// 成功: repo を cwd にした新しい多重化ツールの session で claude を固定の session ID、subagent 用のモデル、本文で起こし、
// claude が前景になってから結果を返す。足し先の session を指定しなければ、新しい session を作る。
func TestRunner_Run(t *testing.T) {
	host := &fakeHost{run: opened, paneGet: claudePane}
	got, err := runner(host).Run(context.Background(), request())
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if want := (Result{SessionID: newSID, PaneID: paneID, Foreground: true}); got != want {
		t.Errorf("Run() = %+v, want %+v", got, want)
	}
	if len(host.runs) != 1 {
		t.Fatalf("NewSession の呼び出し = %d 回, want 1", len(host.runs))
	}
	call := host.runs[0]
	if call.session != "" || call.cwd != repoHome || call.label != "example-repo" {
		t.Errorf("NewSession(%q, %q) (足し先 %q) (label、cwd が違うか、新しい session でない)", call.label, call.cwd, call.session)
	}
	wantArgv := []string{"claude", "--session-id", newSID, "--model", "claude-sonnet-5[1m]", "--", request().Prompt}
	if strings.Join(call.argv, "\x00") != strings.Join(wantArgv, "\x00") {
		t.Errorf("argv = %q, want %q", call.argv, wantArgv)
	}
}

// 足し先の session を指定したときは、新しい session を作らずその session にタブとして起こす。label と argv は
// 新しい session のときと同じ。
func TestRunner_Run_IntoSession(t *testing.T) {
	host := &fakeHost{run: opened, paneGet: claudePane}
	req := request()
	req.Session = "session:01a1e34b-42fc-72ea-b04c-1009e911123c"
	got, err := runner(host).Run(context.Background(), req)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if want := (Result{SessionID: newSID, PaneID: paneID, Foreground: true}); got != want {
		t.Errorf("Run() = %+v, want %+v", got, want)
	}
	if len(host.runs) != 1 {
		t.Fatalf("NewWindow の呼び出し = %d 回, want 1", len(host.runs))
	}
	call := host.runs[0]
	if call.session != req.Session || call.cwd != repoHome || call.label != "example-repo" {
		t.Errorf("NewWindow(%q, %q, %q), want (%q, example-repo, %q)", call.session, call.label, call.cwd, req.Session, repoHome)
	}
	wantArgv := []string{"claude", "--session-id", newSID, "--model", "claude-sonnet-5[1m]", "--", req.Prompt}
	if strings.Join(call.argv, "\x00") != strings.Join(wantArgv, "\x00") {
		t.Errorf("argv = %q, want %q", call.argv, wantArgv)
	}
}

// 起動直後は block が見えない (not found) ことがあるので、待っている間の PaneGet の失敗は数えない。
func TestRunner_Run_WaitsThroughEarlyFailures(t *testing.T) {
	calls := 0
	host := &fakeHost{run: opened, paneGet: func(id string) (multiplexer.Pane, error) {
		calls++
		switch calls {
		case 1:
			return multiplexer.Pane{}, errors.New("block target not found")
		case 2:
			return multiplexer.Pane{PaneID: id}, nil // shell がまだ前景
		default:
			return claudePane(id)
		}
	}}
	if _, err := runner(host).Run(context.Background(), request()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if calls != 3 {
		t.Errorf("PaneGet の呼び出し = %d 回, want 3", calls)
	}
}

func TestRunner_Run_Errors(t *testing.T) {
	tests := []struct {
		name    string
		req     func(Request) Request
		host    *fakeHost
		wantErr string
	}{
		{name: "repo は絶対パス", req: func(r Request) Request { r.Repo = "example-repo"; return r }, wantErr: "絶対パス"},
		{name: "repo が無い", req: func(r Request) Request { r.Repo = "/src/home/missing"; return r }, wantErr: "見つからない"},
		{name: "本文が空なら起こさない", req: func(r Request) Request { r.Prompt = " \n"; return r }, wantErr: "本文"},
		{name: "モデルが無ければ起こさない", req: func(r Request) Request { r.Model = ""; return r }, wantErr: "CLAUDE_CODE_SUBAGENT_MODEL"},
		{
			name:    "session を作れなかった",
			req:     func(r Request) Request { return r },
			host:    &fakeHost{run: func(string, string, []string) (string, error) { return "", errors.New("rex: server error") }},
			wantErr: "server error",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			host := tt.host
			if host == nil {
				host = &fakeHost{}
			}
			_, err := runner(host).Run(context.Background(), tt.req(request()))
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("Run() error = %v, want containing %q", err, tt.wantErr)
			}
			if tt.host == nil && len(host.runs) != 0 {
				t.Errorf("入力の検査で落ちるときは session を作らない: %+v", host.runs)
			}
		})
	}
}

// claude が前景にならなければ、session はできているので error にせず foreground=false で返す。pane は残り、claude が
// 起きているかもしれないので、呼び出し側 (mod) はメモを戻さない (models/handoff の Spawn)。
func TestRunner_Run_NotForeground(t *testing.T) {
	host := &fakeHost{run: opened, paneGet: func(id string) (multiplexer.Pane, error) { return multiplexer.Pane{PaneID: id}, nil }}
	got, err := runner(host).Run(context.Background(), request())
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got.SessionID != newSID || got.PaneID != paneID || got.Foreground {
		t.Errorf("Run() = %+v, want session %s / pane %s / foreground=false", got, newSID, paneID)
	}
	if !strings.Contains(got.Detail, "前景") || !strings.Contains(got.Detail, paneID) {
		t.Errorf("Detail = %q, want 待ち時間と pane", got.Detail)
	}
}

// 既定の NewSessionID は UUID v4 の形。
func TestNewSessionID(t *testing.T) {
	id, err := newSessionID()
	if err != nil {
		t.Fatalf("NewSessionID() error = %v", err)
	}
	parts := strings.Split(id, "-")
	if len(id) != 36 || len(parts) != 5 || parts[2][0] != '4' {
		t.Errorf("NewSessionID() = %q, want UUID v4", id)
	}
}
