package ratelimit

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// TestNew_ディレクトリ名が固定されている は protocol の固定。setup skill の確認手順と
// architecture.md と statusline-state.md がパスを直に書くので固定する。
func TestNew_ディレクトリ名が固定されている(t *testing.T) {
	if got, want := New("/state").Dir, "/state/rate-limits"; got != want {
		t.Errorf("Dir = %q, want %q", got, want)
	}
}

func TestWrite(t *testing.T) {
	observedAt := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)

	t.Run("seven_day があれば付け、ファイルの並びは five_hour、seven_day、observed_at、session_id", func(t *testing.T) {
		s := New(t.TempDir())
		err := s.Write("sess-1", observedAt,
			Window{UsedPercentage: 15, ResetsAt: time.Unix(1786851000, 0)},
			&Window{UsedPercentage: 10, ResetsAt: time.Unix(1787025600, 0)})
		if err != nil {
			t.Fatalf("Write() error = %v", err)
		}
		want := `{"five_hour":{"used_percentage":15,"resets_at":1786851000},` +
			`"seven_day":{"used_percentage":10,"resets_at":1787025600},` +
			`"observed_at":` + itoa(observedAt.Unix()) + `,"session_id":"sess-1"}` + "\n"
		if got := readFileString(t, mustPath(t, s, "sess-1")); got != want {
			t.Errorf("rate-limits file = %q, want %q", got, want)
		}
	})

	t.Run("seven_day が nil なら付けない", func(t *testing.T) {
		s := New(t.TempDir())
		if err := s.Write("sess-1", observedAt, Window{ResetsAt: time.Unix(1786851000, 0)}, nil); err != nil {
			t.Fatalf("Write() error = %v", err)
		}
		want := `{"five_hour":{"used_percentage":0,"resets_at":1786851000},` +
			`"observed_at":` + itoa(observedAt.Unix()) + `,"session_id":"sess-1"}` + "\n"
		if got := readFileString(t, mustPath(t, s, "sess-1")); got != want {
			t.Errorf("rate-limits file = %q, want %q", got, want)
		}
	})

	t.Run("session ID が空か不正ならエラーで何も書かない", func(t *testing.T) {
		root := t.TempDir()
		s := New(root)
		for _, id := range []string{"", "../../etc/passwd"} {
			if err := s.Write(id, observedAt, Window{ResetsAt: time.Unix(1786851000, 0)}, nil); err == nil {
				t.Errorf("Write(%q) がエラーを返さなかった", id)
			}
		}
		if _, err := os.Stat(s.Dir); !os.IsNotExist(err) {
			t.Errorf("rate-limits/ が作られている: %v", err)
		}
	})
}

func TestLoad(t *testing.T) {
	t.Run("正常系: five_hour と observed_at を domain の型へ読む", func(t *testing.T) {
		s := New(t.TempDir())
		writeFile(t, mustPath(t, s, "sess-1"), `{
			"five_hour": {"used_percentage": 14.000000000000002, "resets_at": 1786851000},
			"seven_day": {"used_percentage": 10, "resets_at": 1787025600},
			"observed_at": 1786834771,
			"session_id": "sess-1"
		}`)

		state, err := s.Load("sess-1")
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}
		if state == nil {
			t.Fatal("Load() = nil")
		}
		if got, want := state.FiveHourUsed, 14.000000000000002; got != want {
			t.Errorf("FiveHourUsed = %v, want %v", got, want)
		}
		if got, want := state.FiveHourResetsAt, time.Unix(1786851000, 0); !got.Equal(want) {
			t.Errorf("FiveHourResetsAt = %v, want %v", got, want)
		}
		if got, want := state.ObservedAt, time.Unix(1786834771, 0); !got.Equal(want) {
			t.Errorf("ObservedAt = %v, want %v", got, want)
		}
		if got, want := state.SessionID, "sess-1"; got != want {
			t.Errorf("SessionID = %q, want %q", got, want)
		}
	})

	t.Run("five_hour が null の JSON は HasFiveHour() が false", func(t *testing.T) {
		// 5 時間ウィンドウが切り替わる瞬間、Claude Code は five_hour を null で渡す
		// (実機で 2026-08-27T18:10:01Z に観測)。null は Go では 0 に読まれるため、
		// これを「使用率 0% / resets_at が epoch 0」と解釈してはいけない。
		s := New(t.TempDir())
		writeFile(t, mustPath(t, s, "sess-1"), `{
			"five_hour": {"used_percentage": null, "resets_at": null},
			"seven_day": {"used_percentage": 47, "resets_at": 1788235200},
			"observed_at": 1787818203,
			"session_id": "sess-1"
		}`)

		state, err := s.Load("sess-1")
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}
		if state.HasFiveHour() {
			t.Error("HasFiveHour() = true, want false (five_hour が null)")
		}
	})

	t.Run("使用率 0%% でも resets_at があれば有効", func(t *testing.T) {
		s := New(t.TempDir())
		writeFile(t, mustPath(t, s, "sess-1"), `{
			"five_hour": {"used_percentage": 0, "resets_at": 1786851000},
			"observed_at": 1786834771
		}`)

		state, err := s.Load("sess-1")
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}
		if !state.HasFiveHour() {
			t.Error("HasFiveHour() = false, want true")
		}
	})

	t.Run("ファイルが無いのは正常系 (nil, nil)", func(t *testing.T) {
		s := New(t.TempDir())
		state, err := s.Load("missing")
		if err != nil {
			t.Fatalf("Load() error = %v, want nil", err)
		}
		if state != nil {
			t.Errorf("Load() = %+v, want nil", state)
		}
	})

	t.Run("session ID が空なら判断材料無しとして (nil, nil)", func(t *testing.T) {
		s := New(t.TempDir())
		state, err := s.Load("")
		if err != nil || state != nil {
			t.Errorf("Load(\"\") = %+v, %v, want nil, nil", state, err)
		}
	})

	t.Run("壊れた JSON は不在と区別してエラーにする", func(t *testing.T) {
		s := New(t.TempDir())
		writeFile(t, mustPath(t, s, "sess-1"), `{"five_hour": {`)

		state, err := s.Load("sess-1")
		if err == nil {
			t.Fatal("エラーが返らなかった (壊れた JSON のはず)")
		}
		if state != nil {
			t.Errorf("state = %+v, want nil (エラー時)", state)
		}
		if errors.Is(err, os.ErrNotExist) {
			t.Error("壊れた JSON のエラーが ErrNotExist として扱われている")
		}
	})
}

// nil の State (state 無し) でも HasFiveHour は呼べて false。
func TestHasFiveHour_nil(t *testing.T) {
	var r *State
	if r.HasFiveHour() {
		t.Error("nil の State が true を返した")
	}
}

func mustPath(t *testing.T, s Store, sessionID string) string {
	t.Helper()
	path, err := s.file(sessionID)
	if err != nil {
		t.Fatalf("file(%q) error = %v", sessionID, err)
	}
	return path
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}

func readFileString(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", path, err)
	}
	return string(data)
}

func itoa(n int64) string {
	return strconv.FormatInt(n, 10)
}
