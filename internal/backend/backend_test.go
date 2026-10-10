package backend

import (
	"testing"

	"github.com/usadamasa/agents-daemon/internal/sessionstate"
)

func TestDetect(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{"REX_SESSION があれば rex", map[string]string{"REX_SESSION": "session:01a1"}, sessionstate.MultiplexerKindRex},
		{"herdr の中の rex でも rex", map[string]string{"REX_SESSION": "session:01a1", "HERDR_ENV": "1"}, sessionstate.MultiplexerKindRex},
		{"REX_SESSION が空なら herdr", map[string]string{"REX_SESSION": ""}, sessionstate.MultiplexerKindHerdr},
		{"何も無ければ herdr", nil, sessionstate.MultiplexerKindHerdr},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Detect(func(k string) string { return tt.env[k] }); got != tt.want {
				t.Errorf("Detect() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNew(t *testing.T) {
	store := sessionstate.New(t.TempDir())
	for _, kind := range []string{sessionstate.MultiplexerKindHerdr, sessionstate.MultiplexerKindRex} {
		c, err := New(kind, store)
		if err != nil || c == nil {
			t.Errorf("New(%q) = %v, %v; want client", kind, c, err)
		}
	}
	if _, err := New("tmux", store); err == nil {
		t.Error("New(tmux) error = nil, want 未対応の error")
	}
}
