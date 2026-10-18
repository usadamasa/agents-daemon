package sessionstate

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/usadamasa/agents-daemon/internal/sessioncontext"
	"github.com/usadamasa/agents-daemon/internal/statefile"
)

// statusline の stdin を模した入力。実機の形 (rate_limits は初回 API レスポンス後に
// だけ現れ、ウィンドウの切り替わりでは five_hour が null で届く) をそのまま使う。
const (
	inputFull = `{
		"session_id": "sess-1",
		"context_window": {"total_input_tokens": 126000, "used_percentage": 63.4},
		"rate_limits": {
			"five_hour": {"used_percentage": 14.5, "resets_at": 1786851000},
			"seven_day": {"used_percentage": 10.2, "resets_at": 1787025600}
		}
	}`
	inputFiveHourNull = `{
		"session_id": "sess-1",
		"context_window": {"used_percentage": 70},
		"rate_limits": {
			"five_hour": {"used_percentage": null, "resets_at": null},
			"seven_day": {"used_percentage": 47, "resets_at": 1788235200}
		}
	}`
	inputNoRateLimits = `{"session_id": "sess-1", "context_window": {"used_percentage": 12}}`
)

var ingestNow = time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)

func TestIngestStatusline_context(t *testing.T) {
	clearMultiplexerEnv(t)
	t.Run("rate_limits が無くても context は書く", func(t *testing.T) {
		s := New(t.TempDir())
		if err := s.IngestStatusline([]byte(inputNoRateLimits), ingestNow); err != nil {
			t.Fatalf("IngestStatusline() error = %v", err)
		}

		got := readContextRaw(t, s, "sess-1")
		want := `{"session_id":"sess-1","used_percentage":12,"observed_at":` + itoa(ingestNow.Unix()) + `}` + "\n"
		if got != want {
			t.Errorf("context file = %q, want %q", got, want)
		}
		if _, err := os.Stat(mustPath(t, s, "sess-1")); !os.IsNotExist(err) {
			t.Errorf("rate-limits file が書かれている (rate_limits が無いのに): %v", err)
		}
	})

	t.Run("使用率は整数に丸める", func(t *testing.T) {
		s := New(t.TempDir())
		if err := s.IngestStatusline([]byte(inputFull), ingestNow); err != nil {
			t.Fatalf("IngestStatusline() error = %v", err)
		}
		if got := readContextRaw(t, s, "sess-1"); !strings.Contains(got, `"used_percentage":63,`) {
			t.Errorf("context file = %q, want used_percentage 63 (63.4 を丸めたもの)", got)
		}
	})

	t.Run("transcript_path と context_window_size は写さない (読む側が無い)", func(t *testing.T) {
		s := New(t.TempDir())
		input := `{"session_id": "sess-1", "transcript_path": "/x/t.jsonl", "context_window": {"used_percentage": 12, "context_window_size": 200000}}`
		if err := s.IngestStatusline([]byte(input), ingestNow); err != nil {
			t.Fatalf("IngestStatusline() error = %v", err)
		}
		got := readContextRaw(t, s, "sess-1")
		if strings.Contains(got, "transcript_path") || strings.Contains(got, "context_window_size") {
			t.Errorf("context file = %q, want neither transcript_path nor context_window_size", got)
		}
	})

	t.Run("session_name を写す", func(t *testing.T) {
		s := New(t.TempDir())
		input := `{"session_id": "sess-1", "session_name": "ad-board-cards", "context_window": {"used_percentage": 12}}`
		if err := s.IngestStatusline([]byte(input), ingestNow); err != nil {
			t.Fatalf("IngestStatusline() error = %v", err)
		}
		if got := readContextRaw(t, s, "sess-1"); !strings.Contains(got, `"session_name":"ad-board-cards",`) {
			t.Errorf("context file = %q, want session_name", got)
		}
	})

	t.Run("context_window が無ければ 0 で書く", func(t *testing.T) {
		s := New(t.TempDir())
		if err := s.IngestStatusline([]byte(`{"session_id": "sess-1"}`), ingestNow); err != nil {
			t.Fatalf("IngestStatusline() error = %v", err)
		}
		if got := readContextRaw(t, s, "sess-1"); !strings.Contains(got, `"used_percentage":0,`) {
			t.Errorf("context file = %q, want used_percentage 0", got)
		}
	})

	t.Run("毎回書き直す (前回の値が残らない)", func(t *testing.T) {
		s := New(t.TempDir())
		if err := s.IngestStatusline([]byte(inputFull), ingestNow); err != nil {
			t.Fatalf("1 回目 error = %v", err)
		}
		if err := s.IngestStatusline([]byte(inputNoRateLimits), ingestNow.Add(5*time.Second)); err != nil {
			t.Fatalf("2 回目 error = %v", err)
		}
		got := readContextRaw(t, s, "sess-1")
		want := `{"session_id":"sess-1","used_percentage":12,"observed_at":` + itoa(ingestNow.Unix()+5) + `}` + "\n"
		if got != want {
			t.Errorf("context file = %q, want %q", got, want)
		}
	})
}

func TestIngestStatusline_rateLimits(t *testing.T) {
	t.Run("five_hour.resets_at があれば書き、seven_day も付ける", func(t *testing.T) {
		s := New(t.TempDir())
		if err := s.IngestStatusline([]byte(inputFull), ingestNow); err != nil {
			t.Fatalf("IngestStatusline() error = %v", err)
		}

		raw := readFileString(t, mustPath(t, s, "sess-1"))
		want := `{"five_hour":{"used_percentage":15,"resets_at":1786851000},` +
			`"seven_day":{"used_percentage":10,"resets_at":1787025600},` +
			`"observed_at":` + itoa(ingestNow.Unix()) + `,"session_id":"sess-1"}` + "\n"
		if raw != want {
			t.Errorf("rate-limits file = %q, want %q", raw, want)
		}

		state, err := s.RateLimits.Load("sess-1")
		if err != nil {
			t.Fatalf("RateLimits.Load() error = %v", err)
		}
		if !state.HasFiveHour() {
			t.Fatal("HasFiveHour() = false")
		}
		if got, want := state.FiveHourResetsAt, time.Unix(1786851000, 0); !got.Equal(want) {
			t.Errorf("FiveHourResetsAt = %v, want %v", got, want)
		}
		if state.FiveHourUsed != 15 {
			t.Errorf("FiveHourUsed = %v, want 15 (14.5 を四捨五入)", state.FiveHourUsed)
		}
	})

	t.Run("five_hour が null なら書かず、既存ファイルを残す", func(t *testing.T) {
		// 5 時間ウィンドウの切り替わりで five_hour が null の描画が来る。ここで上書きすると
		// 直前の resets_at が消え、上限待ちの pane の起床時刻が計算できなくなる。
		s := New(t.TempDir())
		if err := s.IngestStatusline([]byte(inputFull), ingestNow); err != nil {
			t.Fatalf("1 回目 error = %v", err)
		}
		before := readFileString(t, mustPath(t, s, "sess-1"))

		if err := s.IngestStatusline([]byte(inputFiveHourNull), ingestNow.Add(time.Minute)); err != nil {
			t.Fatalf("2 回目 error = %v", err)
		}
		if after := readFileString(t, mustPath(t, s, "sess-1")); after != before {
			t.Errorf("rate-limits file が書き換わった:\n before = %q\n after  = %q", before, after)
		}
		// context の方は five_hour と無関係に更新される
		if got := readContextRaw(t, s, "sess-1"); !strings.Contains(got, `"used_percentage":70,`) {
			t.Errorf("context file = %q, want 2 回目の値 70", got)
		}
	})

	t.Run("rate_limits が無ければ書かず、既存ファイルを残す", func(t *testing.T) {
		s := New(t.TempDir())
		writeFile(t, mustPath(t, s, "sess-1"), `{"five_hour": {"used_percentage": 1, "resets_at": 1}}`)

		if err := s.IngestStatusline([]byte(inputNoRateLimits), ingestNow); err != nil {
			t.Fatalf("IngestStatusline() error = %v", err)
		}
		if got := readFileString(t, mustPath(t, s, "sess-1")); got != `{"five_hour": {"used_percentage": 1, "resets_at": 1}}` {
			t.Errorf("既存の rate-limits file が書き換わった: %q", got)
		}
	})

	t.Run("seven_day の resets_at が null なら seven_day を付けない", func(t *testing.T) {
		s := New(t.TempDir())
		input := `{"session_id": "sess-1", "rate_limits": {
			"five_hour": {"used_percentage": null, "resets_at": 1786851000},
			"seven_day": {"used_percentage": null, "resets_at": null}}}`
		if err := s.IngestStatusline([]byte(input), ingestNow); err != nil {
			t.Fatalf("IngestStatusline() error = %v", err)
		}
		raw := readFileString(t, mustPath(t, s, "sess-1"))
		if strings.Contains(raw, "seven_day") {
			t.Errorf("seven_day が書かれている: %q", raw)
		}
		// used_percentage だけ null は実機で見ていないが、読み手は null を 0 に読むので 0 で書く
		if !strings.Contains(raw, `"five_hour":{"used_percentage":0,"resets_at":1786851000}`) {
			t.Errorf("five_hour = %q, want used_percentage 0", raw)
		}
	})
}

func TestIngestStatusline_guards(t *testing.T) {
	t.Run("session_id が空なら何も書かない", func(t *testing.T) {
		root := t.TempDir()
		s := New(root)
		if err := s.IngestStatusline([]byte(`{"context_window": {"used_percentage": 50}}`), ingestNow); err != nil {
			t.Fatalf("IngestStatusline() error = %v", err)
		}
		assertNoFiles(t, root)
	})

	t.Run("session_id が不正ならエラーで何も書かない", func(t *testing.T) {
		root := t.TempDir()
		s := New(root)
		if err := s.IngestStatusline([]byte(`{"session_id": "../../etc/passwd"}`), ingestNow); err == nil {
			t.Fatal("エラーが返らなかった")
		}
		assertNoFiles(t, root)
	})

	t.Run("壊れた JSON はエラーで何も書かない", func(t *testing.T) {
		root := t.TempDir()
		s := New(root)
		if err := s.IngestStatusline([]byte(`{"session_id": "sess-1", `), ingestNow); err == nil {
			t.Fatal("エラーが返らなかった")
		}
		assertNoFiles(t, root)
	})

	t.Run("一時ファイルを残さない (rename で置き換える)", func(t *testing.T) {
		s := New(t.TempDir())
		if err := s.IngestStatusline([]byte(inputFull), ingestNow); err != nil {
			t.Fatalf("IngestStatusline() error = %v", err)
		}
		for _, dir := range []string{s.Context.Dir, s.RateLimits.Dir} {
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatalf("ReadDir(%s) error = %v", dir, err)
			}
			if len(entries) != 1 || entries[0].Name() != "sess-1.json" {
				names := make([]string, 0, len(entries))
				for _, e := range entries {
					names = append(names, e.Name())
				}
				t.Errorf("%s の中身 = %v, want [sess-1.json] だけ", dir, names)
			}
		}
	})
}

func TestIngestStatuslineWithEnv_multiplexer(t *testing.T) {
	observed := itoa(ingestNow.Unix())
	for _, tt := range []struct {
		name string
		env  map[string]string
		want string
	}{
		{
			name: "rex の block と session を書く",
			env:  map[string]string{"REX_SESSION": "session:01a1", "REX_BLOCK": "block:01a1"},
			want: `{"session_id":"sess-1","used_percentage":12,"observed_at":` + observed +
				`,"multiplexer":{"kind":"rex","session":"session:01a1","block":"block:01a1"}}` + "\n",
		},
		{
			name: "多重化ツールの外なら multiplexer を書かない",
			env:  map[string]string{},
			want: `{"session_id":"sess-1","used_percentage":12,"observed_at":` + observed + `}` + "\n",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := New(t.TempDir())
			if err := s.IngestStatuslineWithEnv([]byte(inputNoRateLimits), envOf(tt.env), ingestNow); err != nil {
				t.Fatalf("IngestStatuslineWithEnv() error = %v", err)
			}
			if got := readContextRaw(t, s, "sess-1"); got != tt.want {
				t.Errorf("context file = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestIngestStatusline_環境変数からmultiplexerを読む(t *testing.T) {
	clearMultiplexerEnv(t)
	t.Setenv("REX_SESSION", "session:01a1")
	t.Setenv("REX_BLOCK", "block:01a1")
	s := New(t.TempDir())
	if err := s.IngestStatusline([]byte(inputNoRateLimits), ingestNow); err != nil {
		t.Fatalf("IngestStatusline() error = %v", err)
	}
	got, err := s.Context.ListMultiplexerBlocks(sessioncontext.MultiplexerKindRex)
	if err != nil {
		t.Fatalf("ListMultiplexerBlocks() error = %v", err)
	}
	if want := map[string]string{"block:01a1": "sess-1"}; !reflect.DeepEqual(got, want) {
		t.Errorf("ListMultiplexerBlocks() = %v, want %v", got, want)
	}
}

func readContextRaw(t *testing.T, s Store, sessionID string) string {
	t.Helper()
	path, err := statefile.SessionPath(s.Context.Dir, sessionID, ".json")
	if err != nil {
		t.Fatalf("SessionPath() error = %v", err)
	}
	return readFileString(t, path)
}

func readFileString(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", path, err)
	}
	return string(data)
}

// assertNoFiles は root 配下にファイルが 1 つも無いことを確かめる (ディレクトリだけなら許す)。
func assertNoFiles(t *testing.T, root string) {
	t.Helper()
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			t.Errorf("書かれてはいけないファイルがある: %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("WalkDir() error = %v", err)
	}
}
