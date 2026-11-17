package control_test

import (
	"testing"

	"github.com/usadamasa/agents-daemon/internal/runtime/backend/rex/api/control"
)

func TestSocketPath(t *testing.T) {
	tests := []struct {
		name    string
		server  string
		want    string
		wantErr bool
	}{
		{
			name:   "REX_SERVER の percent-encoded なパスを戻す",
			server: "unix:///Users/u/Library/Application%20Support/rex/server.sock",
			want:   "/Users/u/Library/Application Support/rex/server.sock",
		},
		{
			name: "REX_SERVER が無ければ既定の場所",
			want: "/Users/u/Library/Application Support/rex/server.sock",
		},
		{name: "unix 以外は断る", server: "tcp://127.0.0.1:7000", wantErr: true},
		{name: "パスの無い unix は断る", server: "unix://", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			getenv := func(k string) string {
				if k == "REX_SERVER" {
					return tt.server
				}
				return ""
			}
			got, err := control.SocketPath(getenv, "/Users/u")
			if tt.wantErr {
				if err == nil {
					t.Fatalf("error にならず %q を返した", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("SocketPath: %v", err)
			}
			if got != tt.want {
				t.Errorf("SocketPath = %q、%q のはず", got, tt.want)
			}
		})
	}
}
