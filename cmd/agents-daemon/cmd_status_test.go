package main

import (
	"strings"
	"testing"
	"time"

	"github.com/usadamasa/agents-daemon/internal/daemon"
)

func TestPrintSnapshot_backend(t *testing.T) {
	now := time.Date(2026, 10, 10, 9, 0, 0, 0, time.UTC)
	cases := []struct {
		name    string
		backend string
		want    string
	}{
		{"記録があれば表示する", "rex", "backend: rex"},
		{"変更前の daemon が書いた snapshot は記録なし", "", "backend: (記録なし)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf strings.Builder
			printSnapshot(&buf, &daemon.StatusSnapshot{PID: 42, Backend: tc.backend, StartedAt: now, UpdatedAt: now}, 42, now)
			if !strings.Contains(buf.String(), tc.want) {
				t.Errorf("出力に %q が無い:\n%s", tc.want, buf.String())
			}
		})
	}
}
