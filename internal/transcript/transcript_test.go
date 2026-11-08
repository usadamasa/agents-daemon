package transcript

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testdata(name string) string {
	return filepath.Join("testdata", name)
}

// fixture の entry は 2026 年に実機で踏んだ上限の transcript から、cwd と session ID と request ID を伏せて写したもの。
func TestReadTail_最後の会話のentryを返す(t *testing.T) {
	tests := []struct {
		name     string
		file     string
		wantUUID string
		wantType string
		wantErr  string
	}{
		{"末尾の system と queue-operation を飛ばして error の entry", "limited_five_hour.jsonl", "cae11b0e-b253-403c-8cd6-7cf350809a97", "assistant", "rate_limit"},
		{"再開の user の entry が最後", "resumed.jsonl", "7a1f0c2e-5d3b-4e8f-9a6c-1b2d3e4f5a6b", "user", ""},
		{"isMeta の user と subagent (isSidechain) の entry は会話に数えない", "meta_after_error.jsonl", "cae11b0e-b253-403c-8cd6-7cf350809a97", "assistant", "rate_limit"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, ok, err := ReadTail(testdata(tt.file))
			if err != nil {
				t.Fatalf("ReadTail() error = %v", err)
			}
			if !ok {
				t.Fatal("ReadTail() ok = false, want 会話の entry")
			}
			if e.UUID != tt.wantUUID || e.Type != tt.wantType || e.Error != tt.wantErr {
				t.Errorf("entry = {uuid %s type %s error %q}, want {%s %s %q}", e.UUID, e.Type, e.Error, tt.wantUUID, tt.wantType, tt.wantErr)
			}
		})
	}
}

func TestReadTail_errorのentryの欄を読む(t *testing.T) {
	e, ok, err := ReadTail(testdata("limited_five_hour.jsonl"))
	if err != nil || !ok {
		t.Fatalf("ReadTail() = %v, %v, %v", e, ok, err)
	}
	if !e.IsAPIError || e.APIErrorStatus != 429 || e.Version != "2.1.289" {
		t.Errorf("isApiErrorMessage/apiErrorStatus/version = %v/%d/%s", e.IsAPIError, e.APIErrorStatus, e.Version)
	}
	if want := time.Date(2026, 10, 4, 8, 32, 13, 884_000_000, time.UTC); !e.Timestamp.Equal(want) {
		t.Errorf("timestamp = %v, want %v", e.Timestamp, want)
	}
	if e.Quota == nil {
		t.Fatal("quotaLimits が読めていない")
	}
	if e.Quota.Status != "rejected" || e.Quota.RateLimitType != "five_hour" || e.Quota.OverageStatus != "rejected" {
		t.Errorf("quota = %+v", e.Quota)
	}
	if want := time.Unix(1791111000, 0); !e.Quota.ResetsAt.Equal(want) {
		t.Errorf("resetsAt = %v, want %v", e.Quota.ResetsAt, want)
	}
	if !strings.HasPrefix(e.Text, "You've hit your session limit") {
		t.Errorf("text の頭 = %q", e.Text[:min(len(e.Text), 20)])
	}
}

// errors.jsonl は 1 行 1 つの error の entry。quotaLimits の無い entry は Quota が nil になる。
func TestReadTail_quotaLimitsの無いentry(t *testing.T) {
	entries := readAllConversation(t, testdata("errors.jsonl"))
	if len(entries) != 4 {
		t.Fatalf("entries = %d, want 4", len(entries))
	}
	if entries[0].Quota == nil || entries[0].Quota.RateLimitType != "seven_day" {
		t.Errorf("seven_day の quota = %+v", entries[0].Quota)
	}
	for i, want := range []string{"rate_limit", "rate_limit", "server_error"} {
		e := entries[i+1]
		if e.Quota != nil || e.Error != want || !e.IsAPIError {
			t.Errorf("entries[%d] = {quota %+v error %q api %v}, want quota 無し / %q", i+1, e.Quota, e.Error, e.IsAPIError, want)
		}
	}
	if !strings.HasPrefix(entries[1].Text, "API Error: Request rejected (429)") {
		t.Errorf("429 の text = %q", entries[1].Text)
	}
	if !strings.HasPrefix(entries[2].Text, "You've reached your Fable 5 limit") {
		t.Errorf("Fable の text = %q", entries[2].Text)
	}
}

// readAllConversation は fixture の各行を 1 行だけの transcript として読む。
func readAllConversation(t *testing.T, path string) []Entry {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out []Entry
	for line := range strings.SplitSeq(strings.TrimRight(string(data), "\n"), "\n") {
		p := filepath.Join(t.TempDir(), "one.jsonl")
		if err := os.WriteFile(p, []byte(line+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		e, ok, err := ReadTail(p)
		if err != nil || !ok {
			t.Fatalf("ReadTail(%q の 1 行) = %v, %v", path, ok, err)
		}
		out = append(out, e)
	}
	return out
}

func TestReadTail_会話のentryが無い(t *testing.T) {
	_, ok, err := ReadTail(testdata("no_conversation.jsonl"))
	if err != nil {
		t.Fatalf("ReadTail() error = %v", err)
	}
	if ok {
		t.Error("ok = true, want false (会話の entry が無い)")
	}
}

func TestReadTail_無いファイル(t *testing.T) {
	_, ok, err := ReadTail(filepath.Join(t.TempDir(), "missing.jsonl"))
	if err == nil || ok {
		t.Errorf("ReadTail(無いファイル) = ok %v err %v, want error", ok, err)
	}
}

// Claude Code は transcript を非同期に書く。末尾に改行の無い行は書きかけで、読まずに手前の entry を採る。
func TestReadTail_書きかけの末尾の行は飛ばす(t *testing.T) {
	data, err := os.ReadFile(testdata("resumed.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "t.jsonl")
	partial := string(data) + `{"type":"assistant","uuid":"partial","message":{"role":"assistant","content":[{"type":"te`
	if err := os.WriteFile(p, []byte(partial), 0o600); err != nil {
		t.Fatal(err)
	}
	e, ok, err := ReadTail(p)
	if err != nil || !ok {
		t.Fatalf("ReadTail() = %v, %v", ok, err)
	}
	if e.UUID != "7a1f0c2e-5d3b-4e8f-9a6c-1b2d3e4f5a6b" {
		t.Errorf("uuid = %s, want 書きかけの手前の entry", e.UUID)
	}
}

// tool_result や thinking の 1 行は数十 KB になる。chunk の境界をまたいでも最後の entry を切らずに読む。
func TestReadTail_長い行をchunkの境界をまたいで読む(t *testing.T) {
	data, err := os.ReadFile(testdata("resumed.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	big := strings.Repeat("x", 3*tailChunk+123)
	long := `{"type":"user","uuid":"long-line","timestamp":"2026-10-04T11:00:00.000Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t","content":"` + big + `"}]},"version":"2.1.289"}` + "\n"
	p := filepath.Join(t.TempDir(), "t.jsonl")
	if err := os.WriteFile(p, append(data, long...), 0o600); err != nil {
		t.Fatal(err)
	}
	e, ok, err := ReadTail(p)
	if err != nil || !ok {
		t.Fatalf("ReadTail() = %v, %v", ok, err)
	}
	if e.UUID != "long-line" || e.Type != "user" {
		t.Errorf("entry = {%s %s}, want 長い tool_result の entry", e.UUID, e.Type)
	}
}

func TestReadTail_読める範囲に会話のentryが無ければokはfalse(t *testing.T) {
	p := filepath.Join(t.TempDir(), "t.jsonl")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	// 会話の entry の後ろに、読む範囲の上限を超える量の会話でない行を積む。
	if _, err := f.WriteString(`{"type":"user","uuid":"u","message":{"role":"user","content":"x"}}` + "\n"); err != nil {
		t.Fatal(err)
	}
	filler := `{"type":"queue-operation","content":"` + strings.Repeat("y", 1000) + `"}` + "\n"
	for written := 0; written <= maxTailBytes; written += len(filler) {
		if _, err := f.WriteString(filler); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	_, ok, err := ReadTail(p)
	if err != nil {
		t.Fatalf("ReadTail() error = %v", err)
	}
	if ok {
		t.Error("ok = true, want false (上限の手前で諦める)")
	}
}

func TestStore_Locate(t *testing.T) {
	dir := t.TempDir()
	s := Store{Dir: dir}
	write := func(project, name string, mtime time.Time) string {
		t.Helper()
		p := filepath.Join(dir, project, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, mtime, mtime); err != nil {
			t.Fatal(err)
		}
		return p
	}
	base := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)
	old := write("-Users-x-repo", "sid-a.jsonl", base)
	moved := write("-Users-x-repo--claude-worktrees-fix", "sid-a.jsonl", base.Add(time.Hour))
	write("-Users-x-repo", "sid-b.jsonl", base)
	// subagent の transcript は <sid>/subagents/ の下で、session の transcript ではない。
	write("-Users-x-repo", filepath.Join("sid-c", "subagents", "agent-1.jsonl"), base)

	t.Run("EnterWorktree で project dir が変わっても mtime が新しい方を採る", func(t *testing.T) {
		got, err := s.Locate("sid-a")
		if err != nil {
			t.Fatal(err)
		}
		if got != moved {
			t.Errorf("Locate = %s, want %s (古い方 %s ではない)", got, moved, old)
		}
	})
	t.Run("1 つだけならそれ", func(t *testing.T) {
		got, err := s.Locate("sid-b")
		if err != nil || !strings.HasSuffix(got, "sid-b.jsonl") {
			t.Errorf("Locate = %s, %v", got, err)
		}
	})
	t.Run("無ければ空", func(t *testing.T) {
		got, err := s.Locate("sid-c")
		if err != nil || got != "" {
			t.Errorf("Locate = %q, %v, want 空 (subagents の下は見ない)", got, err)
		}
	})
	t.Run("ファイル名に使えない session ID は error", func(t *testing.T) {
		if _, err := s.Locate("../etc"); err == nil {
			t.Error("error = nil, want ディレクトリの外へ出る ID を弾く")
		}
	})
	t.Run("空の session ID と空の Store は空", func(t *testing.T) {
		if got, err := s.Locate(""); got != "" || err != nil {
			t.Errorf("Locate(\"\") = %q, %v", got, err)
		}
		if got, err := (Store{}).Locate("sid-a"); got != "" || err != nil {
			t.Errorf("Store{}.Locate = %q, %v", got, err)
		}
	})
}

func TestStore_Tail(t *testing.T) {
	dir := t.TempDir()
	s := Store{Dir: dir}
	project := filepath.Join(dir, "-Users-x-repo")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile(testdata("limited_five_hour.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "sid-a.jsonl"), src, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "sid-empty.jsonl"), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name       string
		sid        string
		wantEntry  bool
		wantReason string
	}{
		{"読めれば entry と path", "sid-a", true, ""},
		{"session ID が無い", "", false, "session"},
		{"transcript が無い", "sid-zzz", false, "見つからない"},
		{"会話の entry が無い", "sid-empty", false, "会話の entry"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := s.Tail(tt.sid)
			if (got.Entry != nil) != tt.wantEntry {
				t.Fatalf("Entry = %+v, want あり=%v", got.Entry, tt.wantEntry)
			}
			if tt.wantEntry {
				if got.Path == "" || got.Reason != "" {
					t.Errorf("Tail = {path %q reason %q}", got.Path, got.Reason)
				}
				return
			}
			if !strings.Contains(got.Reason, tt.wantReason) {
				t.Errorf("reason = %q, want %q を含む", got.Reason, tt.wantReason)
			}
		})
	}
}
