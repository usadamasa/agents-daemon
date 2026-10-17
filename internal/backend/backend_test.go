package backend

import (
	"errors"
	"testing"

	"github.com/usadamasa/agents-daemon/internal/sessionstate"
)

func TestDetect(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		want    string
		wantErr error
	}{
		{"REX_SESSION があれば rex", map[string]string{"REX_SESSION": "session:01a1"}, sessionstate.MultiplexerKindRex, nil},
		{"REX_SESSION が空なら rex の外", map[string]string{"REX_SESSION": ""}, "", ErrNoRexSession},
		{"何も無ければ rex の外", nil, "", ErrNoRexSession},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Detect(func(k string) string { return tt.env[k] })
			if got != tt.want || !errors.Is(err, tt.wantErr) {
				t.Errorf("Detect() = %q, %v; want %q, %v", got, err, tt.want, tt.wantErr)
			}
		})
	}
}

func TestNew(t *testing.T) {
	store := sessionstate.New(t.TempDir())
	c, err := New(sessionstate.MultiplexerKindRex, store)
	if err != nil || c == nil {
		t.Errorf("New(rex) = %v, %v; want client", c, err)
	}
	for _, kind := range []string{"herdr", "tmux"} {
		if _, err := New(kind, store); err == nil {
			t.Errorf("New(%q) error = nil, want 未対応の error", kind)
		}
	}
}
