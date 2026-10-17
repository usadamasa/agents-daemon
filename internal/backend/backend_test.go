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

func TestConnect(t *testing.T) {
	store := sessionstate.New(t.TempDir())
	t.Run("rex の中なら rex の client", func(t *testing.T) {
		kind, c, err := Connect(func(string) string { return "session:01a1" }, store)
		if err != nil || c == nil || kind != sessionstate.MultiplexerKindRex {
			t.Errorf("Connect() = %q, %v, %v; want rex, client, nil", kind, c, err)
		}
	})
	t.Run("rex の外なら ErrNoRexSession", func(t *testing.T) {
		_, c, err := Connect(func(string) string { return "" }, store)
		if !errors.Is(err, ErrNoRexSession) || c != nil {
			t.Errorf("Connect() = %v, %v; want nil, ErrNoRexSession", c, err)
		}
	})
}
