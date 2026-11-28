package focus

import (
	"context"
	"errors"
	"strings"
	"testing"
)

const (
	sessionID  = "0d5c7a52-1111-4222-8333-444455556666"
	rexSession = "session:s"
	blockOld   = "block:01a1-old"
	blockNew   = "block:01a2-new"
	windowOld  = "window:old"
	windowNew  = "window:new"
)

// fakeHost は block ごとに居る window を持つ rex。closed の block は session を引けない。shown は表示を頼まれた
// session と window の組。
type fakeHost struct {
	windows map[string]string
	closed  map[string]bool
	showErr error
	shown   [][2]string
}

func (h *fakeHost) ResolveBlock(_ context.Context, blockID string) (string, error) {
	if h.closed[blockID] {
		return "", errors.New("block not found")
	}
	return rexSession, nil
}

func (h *fakeHost) WindowOf(_ context.Context, _, blockID string) (string, string, error) {
	window, ok := h.windows[blockID]
	if !ok {
		return "", "", errors.New("block の居る window が無い")
	}
	return window, "", nil
}

func (h *fakeHost) ShowWindow(_ context.Context, session, window string) error {
	h.shown = append(h.shown, [2]string{session, window})
	return h.showErr
}

func run(h *fakeHost, blocks map[string]string) (Result, error) {
	r := Runner{Host: h, Blocks: func() (map[string]string, error) { return blocks, nil }}
	return r.Run(context.Background(), Request{ToSession: sessionID})
}

// セッションの block の居る window を表示させ、どの pane のどの window かを返す。
func TestRun_ShowsWindowOfSession(t *testing.T) {
	h := &fakeHost{windows: map[string]string{blockNew: windowNew}}
	got, err := run(h, map[string]string{blockNew: sessionID, "block:other": "another-session"})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if want := (Result{PaneID: blockNew, RexSessionID: rexSession, WindowID: windowNew}); got != want {
		t.Errorf("Run() = %+v, want %+v", got, want)
	}
	if len(h.shown) != 1 || h.shown[0] != [2]string{rexSession, windowNew} {
		t.Errorf("表示を頼んだ先 = %v, want [[%s %s]]", h.shown, rexSession, windowNew)
	}
}

// 同じセッションの block が複数あれば、新しい block (ID の順で最後) の window を表示させる。
func TestRun_PrefersNewestBlock(t *testing.T) {
	h := &fakeHost{windows: map[string]string{blockOld: windowOld, blockNew: windowNew}}
	got, err := run(h, map[string]string{blockOld: sessionID, blockNew: sessionID})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got.PaneID != blockNew || len(h.shown) != 1 || h.shown[0][1] != windowNew {
		t.Errorf("Run() = %+v, 表示を頼んだ先 = %v, want %s の %s だけ", got, h.shown, blockNew, windowNew)
	}
}

// 閉じた block と window に置かれていない block は飛ばし、残っている block の window を表示させる。
func TestRun_SkipsBlocksWithoutWindow(t *testing.T) {
	for name, h := range map[string]*fakeHost{
		"閉じた block":        {windows: map[string]string{blockOld: windowOld}, closed: map[string]bool{blockNew: true}},
		"window の無い block": {windows: map[string]string{blockOld: windowOld}},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := run(h, map[string]string{blockOld: sessionID, blockNew: sessionID})
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if got.PaneID != blockOld || len(h.shown) != 1 || h.shown[0][1] != windowOld {
				t.Errorf("Run() = %+v, 表示を頼んだ先 = %v, want %s の %s だけ", got, h.shown, blockOld, windowOld)
			}
		})
	}
}

// セッションの block が記録に無いときと、どの block も window を引けないときは、表示を頼まずに理由を返す。
func TestRun_NoPane(t *testing.T) {
	for name, tt := range map[string]struct {
		host   *fakeHost
		blocks map[string]string
		reason string
	}{
		"記録に無い":         {host: &fakeHost{}, blocks: map[string]string{"block:other": "another-session"}, reason: "見つからない"},
		"どの block も閉じた": {host: &fakeHost{closed: map[string]bool{blockNew: true}}, blocks: map[string]string{blockNew: sessionID}, reason: blockNew},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := run(tt.host, tt.blocks)
			if err == nil || !strings.Contains(err.Error(), tt.reason) {
				t.Errorf("Run() error = %v, want %q を含む理由", err, tt.reason)
			}
			if len(tt.host.shown) != 0 {
				t.Errorf("表示を頼んだ先 = %v, want 無し", tt.host.shown)
			}
		})
	}
}

// 表示を断られたら (Rex.app が繋がっていない、Remote Control が無効)、ほかの block で頼み直さずにその理由を返す。
func TestRun_ShowRefused(t *testing.T) {
	refused := errors.New("Rex.app が断った")
	h := &fakeHost{windows: map[string]string{blockOld: windowOld, blockNew: windowNew}, showErr: refused}
	_, err := run(h, map[string]string{blockOld: sessionID, blockNew: sessionID})
	if !errors.Is(err, refused) {
		t.Errorf("Run() error = %v, want %v", err, refused)
	}
	if len(h.shown) != 1 {
		t.Errorf("表示を頼んだ回数 = %d, want 1", len(h.shown))
	}
}

// block と session の対応を読めなければ、その理由を返す。
func TestRun_BlocksUnreadable(t *testing.T) {
	unreadable := errors.New("読めない")
	r := Runner{Host: &fakeHost{}, Blocks: func() (map[string]string, error) { return nil, unreadable }}
	if _, err := r.Run(context.Background(), Request{ToSession: sessionID}); !errors.Is(err, unreadable) {
		t.Errorf("Run() error = %v, want %v", err, unreadable)
	}
}
