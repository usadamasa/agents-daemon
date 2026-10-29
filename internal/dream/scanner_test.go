package dream

import (
	"strings"
	"testing"
)

func TestNewScanner(t *testing.T) {
	t.Run("大きなバッファで行を読み取れる", func(t *testing.T) {
		data := strings.Repeat("a", 5*1024*1024) + "\n"
		r := strings.NewReader(data)
		s := NewScanner(r)
		if !s.Scan() {
			t.Fatal("スキャンに失敗")
		}
		if len(s.Text()) != 5*1024*1024 {
			t.Errorf("got len=%d, want %d", len(s.Text()), 5*1024*1024)
		}
	})
}
