package backend

import (
	"errors"
	"testing"

	"github.com/usadamasa/agents-daemon/internal/sessioncontext"
)

func TestDetect(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		want    string
		wantErr error
	}{
		// rex は preview.23 から新しい端末に REX_SESSION を入れず、REX_BLOCK だけを入れる。
		{"REX_BLOCK があれば rex", map[string]string{"REX_BLOCK": "block:01a1"}, sessioncontext.MultiplexerKindRex, nil},
		{"REX_BLOCK が空なら rex の外", map[string]string{"REX_BLOCK": ""}, "", ErrNoRexSession},
		{"REX_SESSION だけでは rex の外", map[string]string{"REX_SESSION": "session:01a1"}, "", ErrNoRexSession},
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
	contexts := sessioncontext.New(t.TempDir())
	t.Run("rex の中なら rex の client", func(t *testing.T) {
		kind, c, err := Connect(func(k string) string { return map[string]string{"REX_BLOCK": "block:01a1"}[k] }, contexts)
		if err != nil || c == nil || kind != sessioncontext.MultiplexerKindRex {
			t.Errorf("Connect() = %q, %v, %v; want rex, client, nil", kind, c, err)
		}
	})
	t.Run("rex の外なら ErrNoRexSession", func(t *testing.T) {
		_, c, err := Connect(func(string) string { return "" }, contexts)
		if !errors.Is(err, ErrNoRexSession) || c != nil {
			t.Errorf("Connect() = %v, %v; want nil, ErrNoRexSession", c, err)
		}
	})
}

type closeCounter struct{ n int }

func (c *closeCounter) Close() error {
	c.n++
	return errors.New("server が先に切った")
}

// Close は閉じる口を持つ値だけを閉じ、閉じる失敗は呼び手へ返さない。持たない値は何もしない。
func TestClose(t *testing.T) {
	c := &closeCounter{}
	Close(c)
	if c.n != 1 {
		t.Errorf("Close の回数 = %d, want 1", c.n)
	}
	Close(struct{}{})
}
