package doctor

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/usadamasa/agents-daemon/internal/apppath"
	"github.com/usadamasa/agents-daemon/internal/multiplexer"
	"github.com/usadamasa/agents-daemon/internal/multiplexer/multiplexerfake"
	"github.com/usadamasa/agents-daemon/internal/sessioncontext"
)

// dream の harvest は Stop hook から裏で動き、規約が読めないと止まってもログにしか残らない。
// doctor の節で、規約が読めるかを人の目に入る形で出す。
func TestCollect_DreamPolicy(t *testing.T) {
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	client := &multiplexerfake.Client{
		ReachableFunc: func(context.Context) error { return nil },
		PaneListFunc:  func(context.Context) ([]multiplexer.Pane, error) { return nil, nil },
	}
	cases := []struct {
		name     string
		check    func() ([]string, error)
		want     *DreamPolicyReport
		wantText string
	}{
		{
			name:     "キーが無ければ規約なし",
			check:    func() ([]string, error) { return nil, nil },
			want:     &DreamPolicyReport{},
			wantText: "dream.policy はありません",
		},
		{
			name:     "読めた規約を並べる",
			check:    func() ([]string, error) { return []string{"/p/a.json", "/p/b.json"}, nil },
			want:     &DreamPolicyReport{Sources: []string{"/p/a.json", "/p/b.json"}},
			wantText: "OK: 規約 2 件: /p/a.json, /p/b.json",
		},
		{
			name:     "読めなければ NG と止まるコマンド",
			check:    func() ([]string, error) { return nil, errors.New("規約 /p/a.json: version は 1 にする") },
			want:     &DreamPolicyReport{Error: "規約 /p/a.json: version は 1 にする"},
			wantText: "NG: 規約 /p/a.json: version は 1 にする",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := doctorInput(client, sessioncontext.MultiplexerKindRex, apppath.New(t.TempDir(), "", ""), now)
			in.DreamPolicy = tc.check
			rep, err := Collect(context.Background(), in)
			if err != nil {
				t.Fatal(err)
			}
			if rep.DreamPolicy == nil || rep.DreamPolicy.Error != tc.want.Error ||
				strings.Join(rep.DreamPolicy.Sources, ",") != strings.Join(tc.want.Sources, ",") {
				t.Errorf("dream_policy = %+v; want %+v", rep.DreamPolicy, tc.want)
			}
			var buf bytes.Buffer
			write(&buf, rep)
			if !strings.Contains(buf.String(), "-- dream の規約 --") || !strings.Contains(buf.String(), tc.wantText) {
				t.Errorf("出力に %q が無い:\n%s", tc.wantText, buf.String())
			}
		})
	}
}

// 点検の口が無ければ節ごと省く。
func TestCollect_DreamPolicyOmittedWithoutChecker(t *testing.T) {
	client := &multiplexerfake.Client{
		ReachableFunc: func(context.Context) error { return nil },
		PaneListFunc:  func(context.Context) ([]multiplexer.Pane, error) { return nil, nil },
	}
	in := doctorInput(client, sessioncontext.MultiplexerKindRex, apppath.New(t.TempDir(), "", ""), time.Now())
	rep, err := Collect(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if rep.DreamPolicy != nil {
		t.Errorf("dream_policy = %+v; want nil", rep.DreamPolicy)
	}
	var buf bytes.Buffer
	write(&buf, rep)
	if strings.Contains(buf.String(), "dream の規約") {
		t.Errorf("点検の口が無いのに節が出ている:\n%s", buf.String())
	}
}
