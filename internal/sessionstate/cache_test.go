package sessionstate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// transcript の 1 行を組み立てる｡実機の JSONL は 1 応答が content block ごとに複数行に
// 分かれ (message.id が同じ)､usage.cache_creation に ephemeral_1h / 5m の内訳を持つ｡
type transcriptLine struct {
	Type       string `json:"type"`
	Subtype    string `json:"subtype,omitempty"`
	Timestamp  string `json:"timestamp,omitempty"`
	Sidechain  bool   `json:"isSidechain"`
	ID         string `json:"id,omitempty"`
	Model      string `json:"model,omitempty"`
	OneHour    int    `json:"oneHour,omitempty"`
	FiveMinute int    `json:"fiveMinute,omitempty"`
}

func (l transcriptLine) String() string {
	entry := map[string]any{
		"type":        l.Type,
		"isSidechain": l.Sidechain,
		"uuid":        "u-" + l.ID + "-" + l.Timestamp,
	}
	if l.Timestamp != "" {
		entry["timestamp"] = l.Timestamp
	}
	if l.Subtype != "" {
		entry["subtype"] = l.Subtype
	}
	if l.Type == "assistant" {
		model := l.Model
		if model == "" {
			model = "claude-fable-5-1"
		}
		entry["message"] = map[string]any{
			"id":    l.ID,
			"model": model,
			"usage": map[string]any{
				"input_tokens":                3,
				"cache_read_input_tokens":     1000,
				"cache_creation_input_tokens": l.OneHour + l.FiveMinute,
				"output_tokens":               77,
				"cache_creation": map[string]any{
					"ephemeral_1h_input_tokens": l.OneHour,
					"ephemeral_5m_input_tokens": l.FiveMinute,
				},
			},
		}
	}
	data, err := json.Marshal(entry)
	if err != nil {
		panic(err)
	}
	return string(data)
}

func assistant(ts, id string, oneHour, fiveMinute int) transcriptLine {
	return transcriptLine{Type: "assistant", Timestamp: ts, ID: id, OneHour: oneHour, FiveMinute: fiveMinute}
}

func writeTranscript(t *testing.T, dir string, lines ...string) string {
	t.Helper()
	path := filepath.Join(dir, "transcript.jsonl")
	writeFile(t, path, strings.Join(lines, "\n")+"\n")
	return path
}

// pointTranscript は statusline が書く形で context/sess-1.json に transcript_path を置く｡
func pointTranscript(t *testing.T, s Store, transcriptPath string) {
	t.Helper()
	data, err := json.Marshal(map[string]any{
		"session_id": "sess-1", "used_percentage": 12, "observed_at": 1786834771, "transcript_path": transcriptPath,
	})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	writeFile(t, filepath.Join(s.Context, "sess-1.json"), string(data))
}

// loadFrom は transcript の行から sess-1 の cache を求める｡
func loadFrom(t *testing.T, lines ...string) *Cache {
	t.Helper()
	s := New(t.TempDir())
	pointTranscript(t, s, writeTranscript(t, t.TempDir(), lines...))
	c, err := NewCacheLoader(s).Load("sess-1")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	return c
}

func mustLoadFrom(t *testing.T, lines ...string) *Cache {
	t.Helper()
	c := loadFrom(t, lines...)
	if c == nil {
		t.Fatal("Load() = nil, want cache")
	}
	return c
}

func TestCacheLoader_parse(t *testing.T) {
	t.Run("1 応答の複数行から最も早い timestamp を採り､TTL は最新の write から採る", func(t *testing.T) {
		c := mustLoadFrom(t,
			transcriptLine{Type: "user", Timestamp: "2026-09-27T03:44:00.000Z"}.String(),
			assistant("2026-09-27T03:44:05.954Z", "msg_1", 500, 0).String(),
			assistant("2026-09-27T03:44:09.100Z", "msg_1", 500, 0).String(),
			assistant("2026-09-27T03:44:12.300Z", "msg_1", 500, 0).String(),
		)
		if c.LastRequestRaw != "2026-09-27T03:44:05.954Z" || c.TTL != time.Hour {
			t.Errorf("cache = %+v, want 03:44:05.954Z / 1h", c)
		}
	})

	t.Run("最新の応答の入力トークンの合計を採り､output は含めない", func(t *testing.T) {
		// statusline の used_percentage と同じ式 (input + cache_creation + cache_read)｡
		c := mustLoadFrom(t,
			assistant("2026-09-27T03:40:00.000Z", "msg_1", 500, 0).String(),
			assistant("2026-09-27T03:44:05.954Z", "msg_2", 0, 120).String(),
		)
		if c.ContextTokens != 3+1000+120 {
			t.Errorf("ContextTokens = %d, want %d (msg_2 の入力の合計)", c.ContextTokens, 3+1000+120)
		}
	})

	t.Run("ターンの終わりの応答を採る (Stop hook の時点では書かれていなかった行)", func(t *testing.T) {
		// 実機の probe と同じ形｡tool_use の応答の後に end_turn の応答が 2 行と stop_hook_summary が続く｡
		c := mustLoadFrom(t,
			assistant("2026-09-29T03:36:48.392Z", "msg_tool", 1739, 0).String(),
			transcriptLine{Type: "user", Timestamp: "2026-09-29T03:37:01.182Z"}.String(),
			assistant("2026-09-29T03:37:06.094Z", "msg_end", 80670, 0).String(),
			assistant("2026-09-29T03:37:06.163Z", "msg_end", 80670, 0).String(),
			transcriptLine{Type: "system", Subtype: "stop_hook_summary", Timestamp: "2026-09-29T03:37:06.370Z"}.String(),
		)
		if c.LastRequestRaw != "2026-09-29T03:37:06.094Z" || c.ContextTokens != 3+1000+80670 {
			t.Errorf("cache = %+v, want end_turn の応答 (03:37:06.094Z, %d)", c, 3+1000+80670)
		}
	})

	t.Run("最新の応答が pure hit でも､その前の write の TTL を採る", func(t *testing.T) {
		c := mustLoadFrom(t,
			assistant("2026-09-27T03:40:00.000Z", "msg_1", 0, 700).String(),
			assistant("2026-09-27T03:44:05.954Z", "msg_2", 0, 0).String(),
		)
		if c.LastRequestRaw != "2026-09-27T03:44:05.954Z" || c.TTL != 5*time.Minute {
			t.Errorf("cache = %+v, want 最新の応答の timestamp と前の write の 5m", c)
		}
	})

	t.Run("最新の write が優先され､古い write の TTL には戻らない", func(t *testing.T) {
		c := mustLoadFrom(t,
			assistant("2026-09-27T03:40:00.000Z", "msg_1", 900, 0).String(),
			assistant("2026-09-27T03:44:05.954Z", "msg_2", 0, 120).String(),
		)
		if c.TTL != 5*time.Minute {
			t.Errorf("TTL = %v, want 5m (最新の write)", c.TTL)
		}
	})

	t.Run("1h と 5m の両方に write があれば 1h", func(t *testing.T) {
		if c := mustLoadFrom(t, assistant("2026-09-27T03:44:05.954Z", "msg_1", 10, 10).String()); c.TTL != time.Hour {
			t.Errorf("TTL = %v, want 1h", c.TTL)
		}
	})

	t.Run("sidechain の行は main の応答として数えない", func(t *testing.T) {
		side := assistant("2026-09-27T03:50:00.000Z", "msg_side", 0, 800)
		side.Sidechain = true
		c := mustLoadFrom(t, assistant("2026-09-27T03:44:05.954Z", "msg_1", 500, 0).String(), side.String())
		if c.LastRequestRaw != "2026-09-27T03:44:05.954Z" || c.TTL != time.Hour {
			t.Errorf("cache = %+v, want main の応答 (03:44:05.954Z, 1h)", c)
		}
	})

	t.Run("<synthetic> の行は API リクエストが無いので飛ばす", func(t *testing.T) {
		synthetic := assistant("2026-09-27T03:50:00.000Z", "msg_syn", 0, 0)
		synthetic.Model = "<synthetic>"
		c := mustLoadFrom(t, assistant("2026-09-27T03:44:05.954Z", "msg_1", 500, 0).String(), synthetic.String())
		if c.LastRequestRaw != "2026-09-27T03:44:05.954Z" {
			t.Errorf("LastRequestRaw = %q, want synthetic を飛ばした応答", c.LastRequestRaw)
		}
	})

	t.Run("ミリ秒付きの timestamp を文字列のまま持ち､時刻としても読める", func(t *testing.T) {
		s := New(t.TempDir())
		path := writeTranscript(t, t.TempDir(), assistant("2026-09-27T03:44:05.954Z", "msg_1", 500, 0).String())
		pointTranscript(t, s, path)
		c, err := NewCacheLoader(s).Load("sess-1")
		if err != nil || c == nil {
			t.Fatalf("Load() = %+v, %v", c, err)
		}
		want := time.Date(2026, 9, 27, 3, 44, 5, 954_000_000, time.UTC)
		if !c.LastRequestAt.Equal(want) {
			t.Errorf("LastRequestAt = %v, want %v", c.LastRequestAt, want)
		}
		if c.TranscriptPath != path {
			t.Errorf("TranscriptPath = %q, want %q", c.TranscriptPath, path)
		}
	})

	t.Run("JSON として読めない行は飛ばす", func(t *testing.T) {
		c := mustLoadFrom(t, assistant("2026-09-27T03:44:05.954Z", "msg_1", 500, 0).String(), `{"type":"assistant","message":`, "")
		if c.LastRequestRaw != "2026-09-27T03:44:05.954Z" {
			t.Errorf("LastRequestRaw = %q, want 壊れた行を飛ばした応答", c.LastRequestRaw)
		}
	})

	t.Run("末尾 2MB だけを読み､途中で切れた先頭行を飛ばす", func(t *testing.T) {
		// 2MB を超える 1 行 (user の長い入力) の後に応答が続く形｡seek 後の先頭は
		// この行の途中から始まるので､JSON として読めない｡
		padding := transcriptLine{Type: "user", Timestamp: "2026-09-27T03:00:00.000Z"}.String()
		padding = padding[:len(padding)-1] + `,"pad":"` + strings.Repeat("x", 3*1024*1024) + `"}`
		c := mustLoadFrom(t,
			assistant("2026-09-27T02:00:00.000Z", "msg_0", 0, 900).String(),
			padding,
			assistant("2026-09-27T03:44:05.954Z", "msg_1", 500, 0).String(),
		)
		if c.LastRequestRaw != "2026-09-27T03:44:05.954Z" || c.TTL != time.Hour {
			t.Errorf("cache = %+v, want 末尾の応答 (03:44:05.954Z, 1h)", c)
		}
	})
}

func TestCacheLoader_none(t *testing.T) {
	t.Run("write が 1 つも無ければ nil", func(t *testing.T) {
		if c := loadFrom(t, assistant("2026-09-27T03:44:05.954Z", "msg_1", 0, 0).String()); c != nil {
			t.Errorf("Load() = %+v, want nil", c)
		}
	})

	t.Run("assistant の行が無ければ nil", func(t *testing.T) {
		if c := loadFrom(t, transcriptLine{Type: "user", Timestamp: "2026-09-27T03:44:00.000Z"}.String()); c != nil {
			t.Errorf("Load() = %+v, want nil", c)
		}
	})

	t.Run("compact_boundary の後に応答が無ければ nil (IdleCompactActive が active と見る)", func(t *testing.T) {
		c := loadFrom(t,
			assistant("2026-09-27T03:44:05.954Z", "msg_1", 500, 0).String(),
			transcriptLine{Type: "system", Subtype: "compact_boundary", Timestamp: "2026-09-27T03:50:00.000Z"}.String(),
		)
		if c != nil {
			t.Errorf("Load() = %+v, want nil (compact 前の応答を見ない)", c)
		}
	})

	for _, tt := range []struct {
		name  string
		setup func(t *testing.T, s Store)
	}{
		{"context が無ければ nil", func(*testing.T, Store) {}},
		{"transcript_path が無ければ nil (記録を始める前の版が書いた context)", func(t *testing.T, s Store) {
			writeFile(t, filepath.Join(s.Context, "sess-1.json"), `{"session_id":"sess-1","used_percentage":12,"observed_at":1786834771}`)
		}},
		{"transcript が無ければ nil", func(t *testing.T, s Store) {
			pointTranscript(t, s, filepath.Join(t.TempDir(), "missing.jsonl"))
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := New(t.TempDir())
			tt.setup(t, s)
			c, err := NewCacheLoader(s).Load("sess-1")
			if err != nil || c != nil {
				t.Errorf("Load() = %+v, %v, want nil, nil", c, err)
			}
		})
	}

	t.Run("session ID が空なら nil", func(t *testing.T) {
		c, err := NewCacheLoader(New(t.TempDir())).Load("")
		if err != nil || c != nil {
			t.Errorf("Load(\"\") = %+v, %v, want nil, nil", c, err)
		}
	})

	t.Run("パス区切りを含む session ID を拒否する", func(t *testing.T) {
		if _, err := NewCacheLoader(New(t.TempDir())).Load("../../etc/passwd"); err == nil {
			t.Error("パス区切りを含む session ID が通ってしまった")
		}
	})

	t.Run("壊れた context はエラーにする", func(t *testing.T) {
		s := New(t.TempDir())
		writeFile(t, filepath.Join(s.Context, "sess-1.json"), `{"transcript_path":`)
		if _, err := NewCacheLoader(s).Load("sess-1"); err == nil {
			t.Error("壊れた JSON でエラーが返らなかった")
		}
	})
}

func TestCacheLoader_memo(t *testing.T) {
	first := assistant("2026-09-27T03:44:05.954Z", "msg_1", 500, 0).String()
	// 同じ長さで中身の違う行｡memo が効けば読み直さないので､こちらの値は出ない｡
	second := assistant("2026-09-27T03:44:06.954Z", "msg_2", 500, 0).String()
	if len(first) != len(second) {
		t.Fatalf("fixture の長さが違う: %d != %d", len(first), len(second))
	}

	setup := func(t *testing.T) (*CacheLoader, string, time.Time) {
		t.Helper()
		s := New(t.TempDir())
		path := writeTranscript(t, t.TempDir(), first)
		pointTranscript(t, s, path)
		mtime := time.Date(2026, 9, 27, 3, 45, 0, 0, time.UTC)
		if err := os.Chtimes(path, mtime, mtime); err != nil {
			t.Fatalf("Chtimes() error = %v", err)
		}
		l := NewCacheLoader(s)
		if c, err := l.Load("sess-1"); err != nil || c == nil || c.LastRequestRaw != "2026-09-27T03:44:05.954Z" {
			t.Fatalf("1 回目の Load() = %+v, %v", c, err)
		}
		writeFile(t, path, second+"\n")
		return l, path, mtime
	}

	t.Run("大きさと mtime が同じなら読み直さない", func(t *testing.T) {
		l, path, mtime := setup(t)
		if err := os.Chtimes(path, mtime, mtime); err != nil {
			t.Fatalf("Chtimes() error = %v", err)
		}
		if c, err := l.Load("sess-1"); err != nil || c == nil || c.LastRequestRaw != "2026-09-27T03:44:05.954Z" {
			t.Errorf("Load() = %+v, %v, want 前回の値", c, err)
		}
	})

	t.Run("mtime が変われば読み直す", func(t *testing.T) {
		l, path, mtime := setup(t)
		if err := os.Chtimes(path, mtime.Add(time.Second), mtime.Add(time.Second)); err != nil {
			t.Fatalf("Chtimes() error = %v", err)
		}
		if c, err := l.Load("sess-1"); err != nil || c == nil || c.LastRequestRaw != "2026-09-27T03:44:06.954Z" {
			t.Errorf("Load() = %+v, %v, want 書き換えた後の値", c, err)
		}
	})

	t.Run("Cached は読まずに前回の値を返す", func(t *testing.T) {
		l, _, _ := setup(t)
		if c := l.Cached("sess-1"); c == nil || c.LastRequestRaw != "2026-09-27T03:44:05.954Z" {
			t.Errorf("Cached() = %+v, want 前回の値", c)
		}
		if c := l.Cached("other"); c != nil {
			t.Errorf("Cached(\"other\") = %+v, want nil (読んだことが無い)", c)
		}
	})

	t.Run("Retain は残す session 以外を捨てる", func(t *testing.T) {
		l, _, _ := setup(t)
		l.Retain(map[string]bool{"other": true})
		if c := l.Cached("sess-1"); c != nil {
			t.Errorf("Cached() = %+v, want nil (捨てた)", c)
		}
	})

	t.Run("読めない transcript のエラーは変わるまで 1 回だけ返す", func(t *testing.T) {
		// tick ごとに同じエラーをログへ書かない｡
		s := New(t.TempDir())
		path := writeTranscript(t, t.TempDir(), first)
		pointTranscript(t, s, path)
		if err := os.Chmod(path, 0o000); err != nil {
			t.Fatalf("Chmod() error = %v", err)
		}
		t.Cleanup(func() { _ = os.Chmod(path, 0o600) })
		l := NewCacheLoader(s)
		if _, err := l.Load("sess-1"); err == nil {
			t.Fatal("1 回目: エラーが返らなかった")
		}
		if c, err := l.Load("sess-1"); err != nil || c != nil {
			t.Errorf("2 回目: Load() = %+v, %v, want nil, nil", c, err)
		}
	})
}
