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
	if l.Type == "assistant" {
		model := l.Model
		if model == "" {
			model = "claude-fable-5-1"
		}
		entry["message"] = map[string]any{
			"id":    l.ID,
			"model": model,
			"usage": map[string]any{
				"input_tokens":            3,
				"cache_read_input_tokens": 1000,
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

func stopInput(t *testing.T, sessionID, transcriptPath string) []byte {
	t.Helper()
	data, err := json.Marshal(map[string]any{
		"session_id":       sessionID,
		"transcript_path":  transcriptPath,
		"hook_event_name":  "Stop",
		"stop_hook_active": false,
	})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	return data
}

func ingest(t *testing.T, s Store, transcriptPath string) {
	t.Helper()
	if err := s.IngestStop(stopInput(t, "sess-1", transcriptPath)); err != nil {
		t.Fatalf("IngestStop() error = %v", err)
	}
}

func cachePath(t *testing.T, s Store) string {
	t.Helper()
	path, err := sessionPath(s.Cache, "sess-1", ".json")
	if err != nil {
		t.Fatalf("sessionPath() error = %v", err)
	}
	return path
}

func loadCache(t *testing.T, s Store) *Cache {
	t.Helper()
	c, err := s.LoadCache("sess-1")
	if err != nil {
		t.Fatalf("LoadCache() error = %v", err)
	}
	if c == nil {
		t.Fatal("LoadCache() = nil, want sidecar")
	}
	return c
}

func TestIngestStop_parse(t *testing.T) {
	t.Run("1 応答の複数行から最も早い timestamp を採り､TTL は最新の write から採る", func(t *testing.T) {
		s := New(t.TempDir())
		path := writeTranscript(t, t.TempDir(),
			transcriptLine{Type: "user", Timestamp: "2026-09-27T03:44:00.000Z"}.String(),
			assistant("2026-09-27T03:44:05.954Z", "msg_1", 500, 0).String(),
			assistant("2026-09-27T03:44:09.100Z", "msg_1", 500, 0).String(),
			assistant("2026-09-27T03:44:12.300Z", "msg_1", 500, 0).String(),
		)
		ingest(t, s, path)

		raw := readFileString(t, cachePath(t, s))
		want := `{"last_request_at":"2026-09-27T03:44:05.954Z","ttl_seconds":3600,"transcript_path":` + string(mustJSON(t, path)) + `}` + "\n"
		if raw != want {
			t.Errorf("cache file = %q, want %q", raw, want)
		}
	})

	t.Run("最新の応答が pure hit でも､その前の write の TTL を採る", func(t *testing.T) {
		s := New(t.TempDir())
		path := writeTranscript(t, t.TempDir(),
			assistant("2026-09-27T03:40:00.000Z", "msg_1", 0, 700).String(),
			assistant("2026-09-27T03:44:05.954Z", "msg_2", 0, 0).String(),
		)
		ingest(t, s, path)

		c := loadCache(t, s)
		if c.LastRequestRaw != "2026-09-27T03:44:05.954Z" {
			t.Errorf("LastRequestRaw = %q, want 最新の応答の timestamp", c.LastRequestRaw)
		}
		if c.TTL != 5*time.Minute {
			t.Errorf("TTL = %v, want 5m (前の write から)", c.TTL)
		}
	})

	t.Run("最新の write が優先され､古い write の TTL には戻らない", func(t *testing.T) {
		s := New(t.TempDir())
		path := writeTranscript(t, t.TempDir(),
			assistant("2026-09-27T03:40:00.000Z", "msg_1", 900, 0).String(),
			assistant("2026-09-27T03:44:05.954Z", "msg_2", 0, 120).String(),
		)
		ingest(t, s, path)

		if c := loadCache(t, s); c.TTL != 5*time.Minute {
			t.Errorf("TTL = %v, want 5m (最新の write)", c.TTL)
		}
	})

	t.Run("1h と 5m の両方に write があれば 1h", func(t *testing.T) {
		s := New(t.TempDir())
		path := writeTranscript(t, t.TempDir(),
			assistant("2026-09-27T03:44:05.954Z", "msg_1", 10, 10).String(),
		)
		ingest(t, s, path)

		if c := loadCache(t, s); c.TTL != time.Hour {
			t.Errorf("TTL = %v, want 1h", c.TTL)
		}
	})

	t.Run("sidechain の行は main の応答として数えない", func(t *testing.T) {
		s := New(t.TempDir())
		side := assistant("2026-09-27T03:50:00.000Z", "msg_side", 0, 800)
		side.Sidechain = true
		path := writeTranscript(t, t.TempDir(),
			assistant("2026-09-27T03:44:05.954Z", "msg_1", 500, 0).String(),
			side.String(),
		)
		ingest(t, s, path)

		c := loadCache(t, s)
		if c.LastRequestRaw != "2026-09-27T03:44:05.954Z" || c.TTL != time.Hour {
			t.Errorf("cache = %+v, want main の応答 (03:44:05.954Z, 1h)", c)
		}
	})

	t.Run("<synthetic> の行は API リクエストが無いので飛ばす", func(t *testing.T) {
		s := New(t.TempDir())
		synthetic := assistant("2026-09-27T03:50:00.000Z", "msg_syn", 0, 0)
		synthetic.Model = "<synthetic>"
		path := writeTranscript(t, t.TempDir(),
			assistant("2026-09-27T03:44:05.954Z", "msg_1", 500, 0).String(),
			synthetic.String(),
		)
		ingest(t, s, path)

		if c := loadCache(t, s); c.LastRequestRaw != "2026-09-27T03:44:05.954Z" {
			t.Errorf("LastRequestRaw = %q, want synthetic を飛ばした応答", c.LastRequestRaw)
		}
	})

	t.Run("ミリ秒付きの timestamp を文字列のまま写し､時刻としても読める", func(t *testing.T) {
		s := New(t.TempDir())
		path := writeTranscript(t, t.TempDir(),
			assistant("2026-09-27T03:44:05.954Z", "msg_1", 500, 0).String(),
		)
		ingest(t, s, path)

		c := loadCache(t, s)
		want := time.Date(2026, 9, 27, 3, 44, 5, 954_000_000, time.UTC)
		if !c.LastRequestAt.Equal(want) {
			t.Errorf("LastRequestAt = %v, want %v", c.LastRequestAt, want)
		}
		if c.TranscriptPath != path {
			t.Errorf("TranscriptPath = %q, want %q", c.TranscriptPath, path)
		}
	})

	t.Run("JSON として読めない行は飛ばす", func(t *testing.T) {
		s := New(t.TempDir())
		path := writeTranscript(t, t.TempDir(),
			assistant("2026-09-27T03:44:05.954Z", "msg_1", 500, 0).String(),
			`{"type":"assistant","message":`,
			"",
		)
		ingest(t, s, path)

		if c := loadCache(t, s); c.LastRequestRaw != "2026-09-27T03:44:05.954Z" {
			t.Errorf("LastRequestRaw = %q, want 壊れた行を飛ばした応答", c.LastRequestRaw)
		}
	})

	t.Run("末尾 2MB だけを読み､途中で切れた先頭行を飛ばす", func(t *testing.T) {
		s := New(t.TempDir())
		// 2MB を超える 1 行 (user の長い入力) の後に応答が続く形｡seek 後の先頭は
		// この行の途中から始まるので､JSON として読めない｡
		padding := transcriptLine{Type: "user", Timestamp: "2026-09-27T03:00:00.000Z"}.String()
		padding = padding[:len(padding)-1] + `,"pad":"` + strings.Repeat("x", 3*1024*1024) + `"}`
		path := writeTranscript(t, t.TempDir(),
			assistant("2026-09-27T02:00:00.000Z", "msg_0", 0, 900).String(),
			padding,
			assistant("2026-09-27T03:44:05.954Z", "msg_1", 500, 0).String(),
		)
		ingest(t, s, path)

		c := loadCache(t, s)
		if c.LastRequestRaw != "2026-09-27T03:44:05.954Z" || c.TTL != time.Hour {
			t.Errorf("cache = %+v, want 末尾の応答 (03:44:05.954Z, 1h)", c)
		}
	})
}

func TestIngestStop_noState(t *testing.T) {
	t.Run("write が 1 つも無ければ sidecar を消す", func(t *testing.T) {
		s := New(t.TempDir())
		writeFile(t, cachePath(t, s), `{"last_request_at":"old","ttl_seconds":300,"transcript_path":"x"}`)
		path := writeTranscript(t, t.TempDir(),
			assistant("2026-09-27T03:44:05.954Z", "msg_1", 0, 0).String(),
		)
		ingest(t, s, path)

		if _, err := os.Stat(cachePath(t, s)); !os.IsNotExist(err) {
			t.Errorf("sidecar が残っている: %v", err)
		}
	})

	t.Run("assistant の行が無ければ sidecar を消す (消す物が無くてもエラーにしない)", func(t *testing.T) {
		s := New(t.TempDir())
		path := writeTranscript(t, t.TempDir(),
			transcriptLine{Type: "user", Timestamp: "2026-09-27T03:44:00.000Z"}.String(),
		)
		ingest(t, s, path)

		c, err := s.LoadCache("sess-1")
		if err != nil || c != nil {
			t.Errorf("LoadCache() = %+v, %v, want nil, nil", c, err)
		}
	})

	t.Run("transcript が無ければ既存の sidecar に触らない", func(t *testing.T) {
		s := New(t.TempDir())
		before := `{"last_request_at":"2026-09-27T03:44:05.954Z","ttl_seconds":300,"transcript_path":"x"}`
		writeFile(t, cachePath(t, s), before)
		ingest(t, s, filepath.Join(t.TempDir(), "missing.jsonl"))

		if got := readFileString(t, cachePath(t, s)); got != before {
			t.Errorf("sidecar が書き換わった: %q", got)
		}
	})

	t.Run("transcript_path が空なら何もしない", func(t *testing.T) {
		s := New(t.TempDir())
		before := `{"last_request_at":"2026-09-27T03:44:05.954Z","ttl_seconds":300,"transcript_path":"x"}`
		writeFile(t, cachePath(t, s), before)
		ingest(t, s, "")

		if got := readFileString(t, cachePath(t, s)); got != before {
			t.Errorf("sidecar が書き換わった: %q", got)
		}
	})
}

func TestIngestStop_guards(t *testing.T) {
	t.Run("session_id が空なら何も書かない", func(t *testing.T) {
		root := t.TempDir()
		s := New(root)
		path := writeTranscript(t, t.TempDir(), assistant("2026-09-27T03:44:05.954Z", "msg_1", 500, 0).String())
		if err := s.IngestStop(stopInput(t, "", path)); err != nil {
			t.Fatalf("IngestStop() error = %v", err)
		}
		assertNoFiles(t, root)
	})

	t.Run("session_id が不正ならエラーで何も書かない", func(t *testing.T) {
		root := t.TempDir()
		s := New(root)
		path := writeTranscript(t, t.TempDir(), assistant("2026-09-27T03:44:05.954Z", "msg_1", 500, 0).String())
		if err := s.IngestStop(stopInput(t, "../../etc/passwd", path)); err == nil {
			t.Fatal("エラーが返らなかった")
		}
		assertNoFiles(t, root)
	})

	t.Run("壊れた JSON はエラーで何も書かない", func(t *testing.T) {
		root := t.TempDir()
		s := New(root)
		if err := s.IngestStop([]byte(`{"session_id": "sess-1", `)); err == nil {
			t.Fatal("エラーが返らなかった")
		}
		assertNoFiles(t, root)
	})

	t.Run("一時ファイルを残さない (rename で置き換える)", func(t *testing.T) {
		s := New(t.TempDir())
		path := writeTranscript(t, t.TempDir(), assistant("2026-09-27T03:44:05.954Z", "msg_1", 500, 0).String())
		ingest(t, s, path)

		entries, err := os.ReadDir(s.Cache)
		if err != nil {
			t.Fatalf("ReadDir(%s) error = %v", s.Cache, err)
		}
		if len(entries) != 1 || entries[0].Name() != "sess-1.json" {
			t.Errorf("%s の中身 = %v, want [sess-1.json] だけ", s.Cache, entries)
		}
	})
}

func TestLoadCache(t *testing.T) {
	t.Run("ファイルが無ければ (nil, nil)", func(t *testing.T) {
		s := New(t.TempDir())
		c, err := s.LoadCache("sess-1")
		if err != nil || c != nil {
			t.Errorf("LoadCache() = %+v, %v, want nil, nil", c, err)
		}
	})

	t.Run("session ID が空なら (nil, nil)", func(t *testing.T) {
		s := New(t.TempDir())
		c, err := s.LoadCache("")
		if err != nil || c != nil {
			t.Errorf("LoadCache(\"\") = %+v, %v, want nil, nil", c, err)
		}
	})

	t.Run("パス区切りを含む session ID を拒否する", func(t *testing.T) {
		s := New(t.TempDir())
		if _, err := s.LoadCache("../../etc/passwd"); err == nil {
			t.Error("パス区切りを含む session ID が通ってしまった")
		}
	})

	t.Run("壊れた JSON はエラーにする", func(t *testing.T) {
		s := New(t.TempDir())
		writeFile(t, cachePath(t, s), `{"last_request_at":`)
		if _, err := s.LoadCache("sess-1"); err == nil {
			t.Error("壊れた JSON でエラーが返らなかった")
		}
	})

	t.Run("時刻として読めない last_request_at はエラーにする", func(t *testing.T) {
		s := New(t.TempDir())
		writeFile(t, cachePath(t, s), `{"last_request_at":"yesterday","ttl_seconds":300,"transcript_path":"x"}`)
		if _, err := s.LoadCache("sess-1"); err == nil {
			t.Error("不正な時刻でエラーが返らなかった")
		}
	})
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	return data
}
