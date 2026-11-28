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
	repoHome      = "/src/home/example-repo"
	newSID        = "6b1f3c2a-1111-4222-8333-944455556666"
	givenSID      = "0f2e9c4d-5a6b-4c7d-8e9f-a0b1c2d3e4f5"
	paneID        = "block:01a15e6a-7fe3-7298-af51-8982a808f03b"
	windowID      = "window:01a15e6a-7fe3-7298-af51-8982a808f040"
	createdRexSID = "session:01a15e6a-7fe3-7298-af51-8982a808f000"
	callerRexSID  = "session:01a1e34b-42fc-72ea-b04c-1009e911123c"
	callerBlock   = "block:01a1e34b-42fc-72ea-b04c-1009e9111fff"
	keptRexSID    = "session:01a1e34b-42fc-72ea-b04c-100900000001"
	settingsModel = "claude-sonnet-5[1m]"
	briefPath     = "/src/home/example-repo/tmp/brief-x.md"
	briefText     = "ファイルの依頼文。\n$HOME はそのまま"
)

// fakeHost は Host のテスト用実装。fail が nil でなければ session も タブも作れないことにする。
// block から引ける session は callerBlock の居る callerRexSID だけ。
type fakeHost struct {
	fail    error
	paneGet func(paneID string) (multiplexer.Pane, error)
	// resolved は ResolveBlock に渡った block。
	resolved []string
	// runs は NewSession と NewWindow に渡った引数。NewSession の呼び出しは session が空。
	runs []runCall
}

type runCall struct {
	session    string
	label, cwd string
	argv       []string
}

func (h *fakeHost) NewSession(_ context.Context, label, cwd string, argv []string) (multiplexer.Placement, error) {
	h.runs = append(h.runs, runCall{"", label, cwd, argv})
	if h.fail != nil {
		return multiplexer.Placement{}, h.fail
	}
	return multiplexer.Placement{SessionID: createdRexSID, WindowID: windowID, PaneID: paneID}, nil
}

func (h *fakeHost) NewWindow(_ context.Context, session, label, cwd string, argv []string) (multiplexer.Placement, error) {
	h.runs = append(h.runs, runCall{session, label, cwd, argv})
	if h.fail != nil {
		return multiplexer.Placement{}, h.fail
	}
	return multiplexer.Placement{SessionID: session, WindowID: windowID, PaneID: paneID}, nil
}

func (h *fakeHost) ResolveBlock(_ context.Context, blockID string) (string, error) {
	h.resolved = append(h.resolved, blockID)
	if blockID != callerBlock {
		return "", errors.New(`block "` + blockID + `" not found`)
	}
	return callerRexSID, nil
}

func (h *fakeHost) PaneGet(_ context.Context, id string) (multiplexer.Pane, error) {
	if h.paneGet == nil {
		return claudePane(id)
	}
	return h.paneGet(id)
}

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

// readBrief は briefPath だけを読めることにする。
func readBrief(path string) ([]byte, error) {
	if path == briefPath {
		return []byte(briefText), nil
	}
	return nil, os.ErrNotExist
}

func runner(host *fakeHost) Runner {
	return Runner{
		Host:         host,
		NewSessionID: func() (string, error) { return newSID, nil },
		Stat:         exists,
		ReadFile:     readBrief,
		DefaultModel: func() (string, error) { return settingsModel, nil },
		WaitTimeout:  50 * time.Millisecond,
		WaitInterval: time.Millisecond,
	}
}

func request() Request {
	return Request{Repo: repoHome, Prompt: "メモをタスクにしてください。\n詳細: a"}
}

func argvOf(sid, model, prompt string) []string {
	return []string{"claude", "--session-id", sid, "--model", model, "--", prompt}
}

func sameArgv(a, b []string) bool { return strings.Join(a, "\x00") == strings.Join(b, "\x00") }

// 足し先の既定: 指定 (session、new_session) が無ければ、起こす側の block (CallerBlock) の居る rex session にタブとして足す。
// CallerBlock も無ければ (park hand、rex の外のアプリ) 新しい session を作る。new_session は CallerBlock があっても新しい
// session、session は CallerBlock より優先する。
func TestRunner_Run_Target(t *testing.T) {
	tests := []struct {
		name         string
		req          func(Request) Request
		wantSession  string // NewWindow の足し先。空なら NewSession
		wantRexSID   string
		wantResolved []string // ResolveBlock に渡る block
	}{
		{name: "指定も CallerBlock も無ければ新しい session", req: func(r Request) Request { return r }, wantRexSID: createdRexSID},
		{name: "CallerBlock があればその block の居る session のタブ", req: func(r Request) Request { r.CallerBlock = callerBlock; return r }, wantSession: callerRexSID, wantRexSID: callerRexSID, wantResolved: []string{callerBlock}},
		{name: "session は CallerBlock より優先", req: func(r Request) Request { r.CallerBlock = callerBlock; r.Session = keptRexSID; return r }, wantSession: keptRexSID, wantRexSID: keptRexSID},
		{name: "CallerBlock が無くても session のタブ", req: func(r Request) Request { r.Session = keptRexSID; return r }, wantSession: keptRexSID, wantRexSID: keptRexSID},
		{name: "new_session は CallerBlock があっても新しい session", req: func(r Request) Request { r.CallerBlock = callerBlock; r.NewSession = true; return r }, wantRexSID: createdRexSID},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			host := &fakeHost{}
			got, err := runner(host).Run(context.Background(), tt.req(request()))
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			want := Result{SessionID: newSID, PaneID: paneID, RexSessionID: tt.wantRexSID, WindowID: windowID, Foreground: true}
			if got != want {
				t.Errorf("Run() = %+v, want %+v", got, want)
			}
			if len(host.runs) != 1 {
				t.Fatalf("Host の呼び出し = %d 回, want 1", len(host.runs))
			}
			call := host.runs[0]
			if call.session != tt.wantSession || call.cwd != repoHome || call.label != "example-repo" {
				t.Errorf("Host の呼び出し (足し先 %q, label %q, cwd %q), want (足し先 %q, example-repo, %q)", call.session, call.label, call.cwd, tt.wantSession, repoHome)
			}
			if want := argvOf(newSID, settingsModel, request().Prompt); !sameArgv(call.argv, want) {
				t.Errorf("argv = %q, want %q", call.argv, want)
			}
			if !sameArgv(host.resolved, tt.wantResolved) {
				t.Errorf("ResolveBlock の呼び出し = %q, want %q", host.resolved, tt.wantResolved)
			}
		})
	}
}

// 起こす側の block の居る session を引けなければ、新しい session へ倒さず error にする。
func TestRunner_Run_CallerBlockNotFoundIsError(t *testing.T) {
	host := &fakeHost{}
	req := request()
	req.CallerBlock = "block:gone"
	_, err := runner(host).Run(context.Background(), req)
	if err == nil || !strings.Contains(err.Error(), "block:gone") || !strings.Contains(err.Error(), "not found") {
		t.Errorf("Run() error = %v, want block:gone の not found", err)
	}
	if len(host.runs) != 0 {
		t.Errorf("Host の呼び出し = %+v, want 起こさない", host.runs)
	}
}

// 指した session にタブを足せなければ (閉じた session を含む)、ほかの session へ倒さず error にする。控えた ID が
// 古いことを黙って隠さない。error は session を省けば起こす側の session に足せることを示す。
func TestRunner_Run_SessionNotFoundIsError(t *testing.T) {
	host := &fakeHost{fail: errors.New(`session target "` + keptRexSID + `" not found`)}
	req := request()
	req.CallerBlock = callerBlock
	req.Session = keptRexSID
	_, err := runner(host).Run(context.Background(), req)
	if err == nil || !strings.Contains(err.Error(), "not found") || !strings.Contains(err.Error(), "session を省く") {
		t.Errorf("Run() error = %v, want not found と「session を省く」の案内", err)
	}
	if len(host.runs) != 1 || host.runs[0].session != keptRexSID {
		t.Errorf("Host の呼び出し = %+v, want 指した session への 1 回だけ (CallerBlock へ倒さない)", host.runs)
	}
}

// 起こす側が決めた値 (claude の session_id、model、label) は既定より優先する。
func TestRunner_Run_Overrides(t *testing.T) {
	host := &fakeHost{}
	req := request()
	req.SessionID = givenSID
	req.Model = "sonnet"
	req.Label = "fix-note-link"
	got, err := runner(host).Run(context.Background(), req)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got.SessionID != givenSID {
		t.Errorf("SessionID = %q, want %q", got.SessionID, givenSID)
	}
	call := host.runs[0]
	if call.label != "fix-note-link" {
		t.Errorf("label = %q, want fix-note-link", call.label)
	}
	if want := argvOf(givenSID, "sonnet", req.Prompt); !sameArgv(call.argv, want) {
		t.Errorf("argv = %q, want %q", call.argv, want)
	}
}

// model を渡したときは settings を読まない (settings にキーが無くても起こせる)。
func TestRunner_Run_ModelSkipsSettings(t *testing.T) {
	r := runner(&fakeHost{})
	r.DefaultModel = func() (string, error) { return "", errors.New("settings を読んだ") }
	req := request()
	req.Model = "sonnet"
	if _, err := r.Run(context.Background(), req); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
}

// prompt_file は依頼文をファイルから読む。中身はそのまま本文になる。
func TestRunner_Run_PromptFile(t *testing.T) {
	host := &fakeHost{}
	req := request()
	req.Prompt = ""
	req.PromptFile = briefPath
	if _, err := runner(host).Run(context.Background(), req); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if want := argvOf(newSID, settingsModel, briefText); !sameArgv(host.runs[0].argv, want) {
		t.Errorf("argv = %q, want %q", host.runs[0].argv, want)
	}
}

// fork_from は元の会話を写した新しいセッションとして起こす。session_id は fork 先の ID になり、元の ID は使わない。
func TestRunner_Run_ForkFrom(t *testing.T) {
	host := &fakeHost{}
	req := request()
	req.ForkFrom = givenSID
	got, err := runner(host).Run(context.Background(), req)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got.SessionID != newSID {
		t.Errorf("SessionID = %q, want fork 先の %q", got.SessionID, newSID)
	}
	want := []string{"claude", "--resume", givenSID, "--fork-session", "--session-id", newSID, "--model", settingsModel, "--", req.Prompt}
	if !sameArgv(host.runs[0].argv, want) {
		t.Errorf("argv = %q, want %q", host.runs[0].argv, want)
	}
}

// 起動直後は block が見えない (not found) ことがあるので、待っている間の PaneGet の失敗は数えない。
func TestRunner_Run_WaitsThroughEarlyFailures(t *testing.T) {
	calls := 0
	host := &fakeHost{paneGet: func(id string) (multiplexer.Pane, error) {
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
	noModel := func() (string, error) { return "", nil }
	tests := []struct {
		name         string
		req          func(Request) Request
		defaultModel func() (string, error)
		host         *fakeHost
		wantErr      string
	}{
		{name: "repo は絶対パス", req: func(r Request) Request { r.Repo = "example-repo"; return r }, wantErr: "絶対パス"},
		{name: "repo が無い", req: func(r Request) Request { r.Repo = "/src/home/missing"; return r }, wantErr: "見つからない"},
		{name: "本文が空なら起こさない", req: func(r Request) Request { r.Prompt = " \n"; return r }, wantErr: "本文"},
		{name: "prompt と prompt_file の両方は受けない", req: func(r Request) Request { r.PromptFile = briefPath; return r }, wantErr: "prompt_file"},
		{name: "prompt_file が読めない", req: func(r Request) Request { r.Prompt = ""; r.PromptFile = "/src/home/missing.md"; return r }, wantErr: "missing.md"},
		{name: "モデルが無ければ起こさない", req: func(r Request) Request { return r }, defaultModel: noModel, wantErr: "CLAUDE_CODE_SUBAGENT_MODEL"},
		{name: "session_id は UUID", req: func(r Request) Request { r.SessionID = "not-a-uuid"; return r }, wantErr: "UUID"},
		{name: "fork_from は UUID", req: func(r Request) Request { r.ForkFrom = "not-a-uuid"; return r }, wantErr: "fork_from"},
		{name: "fork_from と session_id が同じなら起こさない", req: func(r Request) Request { r.ForkFrom = givenSID; r.SessionID = givenSID; return r }, wantErr: "fork_from"},
		{name: "session と new_session の両方は受けない", req: func(r Request) Request { r.Session = keptRexSID; r.NewSession = true; return r }, wantErr: "new_session"},
		{
			name:    "session を作れなかった",
			req:     func(r Request) Request { return r },
			host:    &fakeHost{fail: errors.New("rex: server error")},
			wantErr: "server error",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			host := tt.host
			if host == nil {
				host = &fakeHost{fail: errors.New("入力の検査で落ちるはず")}
			}
			r := runner(host)
			if tt.defaultModel != nil {
				r.DefaultModel = tt.defaultModel
			}
			_, err := r.Run(context.Background(), tt.req(request()))
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
	host := &fakeHost{paneGet: func(id string) (multiplexer.Pane, error) { return multiplexer.Pane{PaneID: id}, nil }}
	got, err := runner(host).Run(context.Background(), request())
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got.SessionID != newSID || got.PaneID != paneID || got.RexSessionID != createdRexSID || got.WindowID != windowID || got.Foreground {
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
