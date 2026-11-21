package tablabel

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// fakeHost は window を 1 つ持つ rex。sets は付けた名前を順に持つ。
type fakeHost struct {
	window, label string
	missing       bool
	setErr        error
	sets          []string
}

func (h *fakeHost) WindowOf(_ context.Context, session, block string) (string, string, error) {
	if h.missing {
		return "", "", errors.New("window not found")
	}
	return h.window, h.label, nil
}

func (h *fakeHost) SetWindowLabel(_ context.Context, session, window, label string) error {
	if h.setErr != nil {
		return h.setErr
	}
	h.sets = append(h.sets, label)
	h.label = label
	return nil
}

const (
	session = "session:s"
	block   = "block:b"
	window  = "window:w"
)

func run(t *testing.T, h *fakeHost, req Request) (Result, error) {
	t.Helper()
	return Runner{Host: h}.Run(context.Background(), req)
}

// label を書かなければ今の名前を読むだけで、付けない。
func TestRun_Get(t *testing.T) {
	h := &fakeHost{window: window, label: "release-restart-verify"}
	got, err := run(t, h, Request{RexSession: session, RexBlock: block})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got != (Result{WindowID: window, Label: "release-restart-verify", Status: StatusCurrent}) || len(h.sets) != 0 {
		t.Errorf("Run() = %+v, sets = %v", got, h.sets)
	}
}

// label を書けば、今の名前が人の付けたものでも付け直す。
func TestRun_Rename(t *testing.T) {
	h := &fakeHost{window: window, label: "my-own-name"}
	got, err := run(t, h, Request{RexSession: session, RexBlock: block, Label: "tab-label-sync"})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got != (Result{WindowID: window, Label: "tab-label-sync", Status: StatusRenamed}) || len(h.sets) != 1 {
		t.Errorf("Run() = %+v, sets = %v", got, h.sets)
	}
}

// 付ける名前が今と同じなら付けない。
func TestRun_Unchanged(t *testing.T) {
	h := &fakeHost{window: window, label: "tab-label-sync"}
	got, err := run(t, h, Request{RexSession: session, RexBlock: block, Label: "tab-label-sync"})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got != (Result{WindowID: window, Label: "tab-label-sync", Status: StatusUnchanged}) || len(h.sets) != 0 {
		t.Errorf("Run() = %+v, sets = %v", got, h.sets)
	}
}

// 名前は 1 行で 64 文字まで。前後の空白と制御文字は付けない (rex は制御文字を bad_request で拒む)。
func TestRun_InvalidLabel(t *testing.T) {
	for _, label := range []string{
		" 前に空白", "後ろに空白 ", "改行\nを含む", "タブ\tを含む", "制御\x07文字", strings.Repeat("あ", 65),
	} {
		h := &fakeHost{window: window, label: "old-name"}
		if _, err := run(t, h, Request{RexSession: session, RexBlock: block, Label: label}); err == nil {
			t.Errorf("Run(label=%q) error = nil", label)
		}
		if len(h.sets) != 0 {
			t.Errorf("Run(label=%q) sets = %v", label, h.sets)
		}
	}
	for _, label := range []string{"タブ名の追従", "Go のソートの違い", "fix-login-redirect", "Python の dataclass の機能の説明", "Go の slices.Sort と sort.Slice の違い", strings.Repeat("あ", 64)} {
		h := &fakeHost{window: window, label: "old-name"}
		if _, err := run(t, h, Request{RexSession: session, RexBlock: block, Label: label}); err != nil {
			t.Errorf("Run(label=%q) error = %v", label, err)
		}
	}
}

// 置き場が欠けた依頼と、window を引けない・付けられない失敗は error にする。
func TestRun_Failures(t *testing.T) {
	for name, tt := range map[string]struct {
		h   *fakeHost
		req Request
	}{
		"session 無し":   {&fakeHost{window: window}, Request{RexBlock: block}},
		"block 無し":     {&fakeHost{window: window}, Request{RexSession: session}},
		"window を引けない": {&fakeHost{missing: true}, Request{RexSession: session, RexBlock: block}},
		"付けられない":       {&fakeHost{window: window, label: "old-name", setErr: errors.New("boom")}, Request{RexSession: session, RexBlock: block, Label: "new-name"}},
	} {
		if _, err := run(t, tt.h, tt.req); err == nil {
			t.Errorf("%s: error = nil", name)
		}
	}
}
