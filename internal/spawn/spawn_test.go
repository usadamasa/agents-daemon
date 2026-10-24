package spawn

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/usadamasa/agents-daemon/internal/domain"
	"github.com/usadamasa/agents-daemon/internal/multiplexer"
)

const (
	repoHome  = "/src/home/claude-config"
	repoWork  = "/src/work/service"
	openerCWD = "/src/home/agents-daemon"
	openerSID = "0d5e717f-82c5-4e71-ac74-799f7197e0de"
	newSID    = "6b1f3c2a-1111-4222-8333-944455556666"
	rexSess   = "session:01a15e43-8a99-736e-ae8e-bcca535204e4"
	paneID    = "block:01a15e6a-7fe3-7298-af51-8982a808f03b"
)

// fakeHost は Host のテスト用実装。run が nil なら window を作れないことにする。
type fakeHost struct {
	run     func(session, cwd, label string, argv []string) (string, error)
	paneGet func(paneID string) (multiplexer.Pane, error)
	// runs は RunWindow に渡った引数。
	runs []runCall
}

type runCall struct {
	session, cwd, label string
	argv                []string
}

func (h *fakeHost) RunWindow(_ context.Context, session, cwd, label string, argv []string) (string, error) {
	h.runs = append(h.runs, runCall{session, cwd, label, argv})
	if h.run == nil {
		return "", errors.New("fakeHost: run が未設定")
	}
	return h.run(session, cwd, label, argv)
}

func (h *fakeHost) PaneGet(_ context.Context, id string) (multiplexer.Pane, error) {
	if h.paneGet == nil {
		return multiplexer.Pane{}, errors.New("fakeHost: paneGet が未設定")
	}
	return h.paneGet(id)
}

func opened(string, string, string, []string) (string, error) { return paneID, nil }

func claudePane(id string) (multiplexer.Pane, error) {
	return multiplexer.Pane{PaneID: id, Agent: "claude", AgentStatus: multiplexer.AgentStatusUnknown}, nil
}

// classify は /src/home を私用、それ以外を業務にする。
func classify(_ string, cwd string) domain.Domain {
	if strings.HasPrefix(cwd, "/src/home/") {
		return domain.Home
	}
	return domain.Work
}

// exists は repoHome と repoWork だけがディレクトリとして存在することにする。
func exists(path string) (os.FileInfo, error) {
	if path == repoHome || path == repoWork {
		return os.Stat(".")
	}
	return nil, os.ErrNotExist
}

func runner(host *fakeHost) Runner {
	return Runner{
		Host:         host,
		Domain:       classify,
		NewSessionID: func() (string, error) { return newSID, nil },
		Stat:         exists,
		WaitTimeout:  50 * time.Millisecond,
		WaitInterval: time.Millisecond,
	}
}

func request() Request {
	return Request{Repo: repoHome, SessionID: openerSID, CWD: openerCWD, Prompt: "メモをタスクにしてください。\n詳細: a", Model: "claude-sonnet-5[1m]", MultiplexerSession: rexSess}
}

// 成功: repo を cwd にした window で claude を固定の session ID、subagent 用のモデル、本文で起こし、claude が前景になってから
// 結果を返す。
func TestRunner_Run(t *testing.T) {
	host := &fakeHost{run: opened, paneGet: claudePane}
	got, err := runner(host).Run(context.Background(), request())
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if want := (Result{SessionID: newSID, PaneID: paneID}); got != want {
		t.Errorf("Run() = %+v, want %+v", got, want)
	}
	if len(host.runs) != 1 {
		t.Fatalf("RunWindow の呼び出し = %d 回, want 1", len(host.runs))
	}
	call := host.runs[0]
	if call.session != rexSess || call.cwd != repoHome || call.label != "claude-config" {
		t.Errorf("RunWindow(%q, %q, %q) (session、cwd、label が違う)", call.session, call.cwd, call.label)
	}
	wantArgv := []string{"claude", "--session-id", newSID, "--model", "claude-sonnet-5[1m]", "--", request().Prompt}
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
		{name: "repo は絶対パス", req: func(r Request) Request { r.Repo = "claude-config"; return r }, wantErr: "絶対パス"},
		{name: "repo が無い", req: func(r Request) Request { r.Repo = "/src/home/missing"; return r }, wantErr: "見つからない"},
		{name: "私用のボードから業務の repo へは起こさない", req: func(r Request) Request { r.Repo = repoWork; return r }, wantErr: "domain"},
		{name: "本文が空なら起こさない", req: func(r Request) Request { r.Prompt = " \n"; return r }, wantErr: "本文"},
		{name: "モデルが無ければ起こさない", req: func(r Request) Request { r.Model = ""; return r }, wantErr: "CLAUDE_CODE_SUBAGENT_MODEL"},
		{
			name:    "window を作れなかった",
			req:     func(r Request) Request { return r },
			host:    &fakeHost{run: func(string, string, string, []string) (string, error) { return "", errors.New("rex: server error") }},
			wantErr: "server error",
		},
		{
			name:    "claude が前景にならなければ error (pane は残る)",
			req:     func(r Request) Request { return r },
			host:    &fakeHost{run: opened, paneGet: func(id string) (multiplexer.Pane, error) { return multiplexer.Pane{PaneID: id}, nil }},
			wantErr: "前景",
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
				t.Errorf("入力の検査で落ちるときは window を作らない: %+v", host.runs)
			}
		})
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
