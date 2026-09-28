package sessionstate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ttlGuardFixture は 1 セッションぶんの transcript と Store を持つ｡now は mtime の判定と
// 合わせるため実時刻を使い､transcript の timestamp は now からの相対で組み立てる｡
type ttlGuardFixture struct {
	t          *testing.T
	s          Store
	now        time.Time
	transcript string
}

func newTTLGuardFixture(t *testing.T) *ttlGuardFixture {
	t.Helper()
	return &ttlGuardFixture{
		t:          t,
		s:          New(t.TempDir()),
		now:        time.Now(),
		transcript: filepath.Join(t.TempDir(), "transcript.jsonl"),
	}
}

// ago は now から minutes 分前の timestamp を transcript と同じ書式 (ミリ秒付き､Z) で返す｡
func (f *ttlGuardFixture) ago(minutes float64) string {
	return f.now.Add(-time.Duration(minutes * float64(time.Minute))).UTC().Format("2006-01-02T15:04:05.000Z")
}

func (f *ttlGuardFixture) write(lines ...transcriptLine) {
	f.t.Helper()
	strs := make([]string, len(lines))
	for i, l := range lines {
		strs[i] = l.String()
	}
	writeFile(f.t, f.transcript, strings.Join(strs, "\n")+"\n")
}

func (f *ttlGuardFixture) run(prompt string) string {
	f.t.Helper()
	warning, err := f.s.TTLGuard(f.input("sess-1", prompt), f.now)
	if err != nil {
		f.t.Fatalf("TTLGuard() error = %v", err)
	}
	return warning
}

func (f *ttlGuardFixture) input(sessionID, prompt string) []byte {
	f.t.Helper()
	data, err := json.Marshal(map[string]any{
		"session_id":      sessionID,
		"transcript_path": f.transcript,
		"hook_event_name": "UserPromptSubmit",
		"prompt":          prompt,
	})
	if err != nil {
		f.t.Fatalf("Marshal() error = %v", err)
	}
	return data
}

func (f *ttlGuardFixture) ackPath() string {
	return filepath.Join(f.s.CacheAck, "sess-1")
}

func (f *ttlGuardFixture) assertPass(prompt string) {
	f.t.Helper()
	if w := f.run(prompt); w != "" {
		f.t.Errorf("TTLGuard() = %q, want 通す (空)", w)
	}
}

func (f *ttlGuardFixture) assertBlock(prompt string) string {
	f.t.Helper()
	w := f.run(prompt)
	if w == "" {
		f.t.Error("TTLGuard() = 空 (通した), want 止めて警告")
	}
	return w
}

func boundary(ts string) transcriptLine {
	return transcriptLine{Type: "system", Timestamp: ts, Subtype: "compact_boundary"}
}

// 参考実装 (claude-token-audit の tests/test_ttl_guard.py) の 14 ケース｡
func TestTTLGuard_reference(t *testing.T) {
	t.Run("TTL 内は通す", func(t *testing.T) {
		f := newTTLGuardFixture(t)
		f.write(assistant(f.ago(30), "m1", 1000, 0))
		f.assertPass("hello")
	})

	t.Run("1h の失効で 1 回止め､再送は通す", func(t *testing.T) {
		f := newTTLGuardFixture(t)
		f.write(assistant(f.ago(61), "m1", 1000, 0))
		w := f.assertBlock("hello")
		for _, want := range []string{"61 分", "60 分", "/clear", "/compact"} {
			if !strings.Contains(w, want) {
				t.Errorf("警告に %q が無い: %q", want, w)
			}
		}
		if strings.Contains(w, "promptCacheTtl") {
			t.Errorf("1h なのに promptCacheTtl を案内している: %q", w)
		}
		f.assertPass("hello")
	})

	t.Run("5m の TTL なら promptCacheTtl を案内する", func(t *testing.T) {
		f := newTTLGuardFixture(t)
		f.write(assistant(f.ago(6), "m1", 0, 1000))
		w := f.assertBlock("hello")
		for _, want := range []string{"5 分", `"promptCacheTtl": "1h"`, "CLAUDE_CODE_PROMPT_CACHE_TTL"} {
			if !strings.Contains(w, want) {
				t.Errorf("警告に %q が無い: %q", want, w)
			}
		}
	})

	t.Run("TTL は最新の行ではなく最新の write から採る", func(t *testing.T) {
		f := newTTLGuardFixture(t)
		f.write(
			assistant(f.ago(59), "m1", 1000, 0),
			assistant(f.ago(10), "m2", 0, 0),
		)
		f.assertPass("hello")
	})

	t.Run("スラッシュコマンドは止めない", func(t *testing.T) {
		f := newTTLGuardFixture(t)
		f.write(assistant(f.ago(61), "m1", 1000, 0))
		f.assertPass("/compact keep the plan")
		f.assertPass("  /clear")
		if _, err := os.Stat(f.ackPath()); !os.IsNotExist(err) {
			t.Errorf("スラッシュで ack が書かれている (err = %v)", err)
		}
	})

	t.Run("sidechain の行は無視する", func(t *testing.T) {
		f := newTTLGuardFixture(t)
		side := assistant(f.ago(1), "a1", 0, 1000)
		side.Sidechain = true
		f.write(assistant(f.ago(61), "m1", 1000, 0), side)
		if w := f.assertBlock("hello"); !strings.Contains(w, "60 分") {
			t.Errorf("警告が main の 1h を見ていない: %q", w)
		}
	})

	t.Run("synthetic の行は無視する", func(t *testing.T) {
		f := newTTLGuardFixture(t)
		synthetic := assistant(f.ago(1), "syn", 0, 0)
		synthetic.Model = "<synthetic>"
		f.write(assistant(f.ago(61), "m1", 1000, 0), synthetic)
		f.assertBlock("hello")
	})

	t.Run("cache write が無ければ何もしない", func(t *testing.T) {
		f := newTTLGuardFixture(t)
		f.write(assistant(f.ago(120), "m1", 0, 0))
		f.assertPass("hello")
	})

	t.Run("transcript が無ければ何もしない", func(t *testing.T) {
		f := newTTLGuardFixture(t)
		f.assertPass("hello")
	})

	t.Run("58/60 分はまだ warm なので通す", func(t *testing.T) {
		f := newTTLGuardFixture(t)
		f.write(assistant(f.ago(58), "m1", 1000, 0))
		f.assertPass("hello")
	})

	t.Run("ack は時間で失効しない", func(t *testing.T) {
		f := newTTLGuardFixture(t)
		f.write(assistant(f.ago(61), "m1", 1000, 0))
		f.assertBlock("hello")
		ageFile(t, f.ackPath(), time.Hour, f.now)
		f.assertPass("hello")
	})

	t.Run("新しい idle gap では再び止める", func(t *testing.T) {
		f := newTTLGuardFixture(t)
		f.write(assistant(f.ago(130), "m1", 1000, 0))
		f.assertBlock("hello")
		f.assertPass("hello")
		// 直前の ack を daemon の送信と取り違えないよう mtime を古くしておく｡
		ageFile(t, f.ackPath(), time.Hour, f.now)
		f.write(
			assistant(f.ago(130), "m1", 1000, 0),
			assistant(f.ago(65), "m2", 1000, 0),
		)
		f.assertBlock("hello")
	})

	t.Run("失効は応答の開始時刻から数える", func(t *testing.T) {
		f := newTTLGuardFixture(t)
		f.write(
			assistant(f.ago(60.5), "m1", 1000, 0),
			assistant(f.ago(59.5), "m1", 1000, 0),
		)
		f.assertBlock("hello")
	})

	t.Run("90 日より古い ack を掃除する", func(t *testing.T) {
		f := newTTLGuardFixture(t)
		old := filepath.Join(f.s.CacheAck, "old")
		keep := filepath.Join(f.s.CacheAck, "keep")
		writeFile(t, old, "x")
		writeFile(t, keep, "x")
		ageFile(t, old, 91*24*time.Hour, f.now)
		ageFile(t, keep, 89*24*time.Hour, f.now)
		f.write(assistant(f.ago(1), "m1", 1000, 0))
		f.assertPass("hello")
		if _, err := os.Stat(old); !os.IsNotExist(err) {
			t.Errorf("91 日前の ack が残っている (err = %v)", err)
		}
		if _, err := os.Stat(keep); err != nil {
			t.Errorf("89 日前の ack が消えた: %v", err)
		}
	})
}

// daemon の ack との取り決め (issue #11 とそのコメント)｡
func TestTTLGuard_ack(t *testing.T) {
	t.Run("止めたときの ack は transcript の timestamp の文字列そのまま (末尾改行なし)", func(t *testing.T) {
		f := newTTLGuardFixture(t)
		ts := f.ago(61)
		f.write(assistant(ts, "m1", 1000, 0))
		f.assertBlock("hello")
		if got := readFileString(t, f.ackPath()); got != ts {
			t.Errorf("ack = %q, want %q", got, ts)
		}
	})

	t.Run("daemon が書いた同じ値の ack は通す", func(t *testing.T) {
		f := newTTLGuardFixture(t)
		ts := f.ago(300)
		f.write(assistant(ts, "m1", 1000, 0))
		writeFile(t, f.ackPath(), ts)
		ageFile(t, f.ackPath(), 10*time.Minute, f.now)
		f.assertPass("hello")
	})

	t.Run("mtime が直近の sentinel は通し､実際の値で書き換える", func(t *testing.T) {
		f := newTTLGuardFixture(t)
		ts := f.ago(300)
		f.write(assistant(ts, "m1", 1000, 0))
		writeFile(t, f.ackPath(), CacheAckUnknown)
		ageFile(t, f.ackPath(), 10*time.Second, f.now)
		f.assertPass("hello")
		if got := readFileString(t, f.ackPath()); got != ts {
			t.Errorf("ack = %q, want 実際の値 %q", got, ts)
		}
	})

	t.Run("mtime が古い sentinel は止める", func(t *testing.T) {
		f := newTTLGuardFixture(t)
		ts := f.ago(300)
		f.write(assistant(ts, "m1", 1000, 0))
		writeFile(t, f.ackPath(), CacheAckUnknown)
		ageFile(t, f.ackPath(), 10*time.Minute, f.now)
		f.assertBlock("hello")
		if got := readFileString(t, f.ackPath()); got != ts {
			t.Errorf("ack = %q, want 実際の値 %q", got, ts)
		}
	})

	t.Run("mtime が直近なら値が食い違っても通す (sidecar が中断で古いまま)", func(t *testing.T) {
		f := newTTLGuardFixture(t)
		ts := f.ago(300)
		f.write(
			assistant(f.ago(400), "m0", 1000, 0),
			assistant(ts, "m1", 1000, 0),
		)
		writeFile(t, f.ackPath(), f.ago(400))
		ageFile(t, f.ackPath(), 10*time.Second, f.now)
		f.assertPass("hello")
		if got := readFileString(t, f.ackPath()); got != ts {
			t.Errorf("ack = %q, want 実際の値 %q", got, ts)
		}
	})

	t.Run("warm なら ack に触らない", func(t *testing.T) {
		f := newTTLGuardFixture(t)
		f.write(assistant(f.ago(30), "m1", 1000, 0))
		f.assertPass("hello")
		if _, err := os.Stat(f.ackPath()); !os.IsNotExist(err) {
			t.Errorf("warm で ack が書かれている (err = %v)", err)
		}
	})
}

func TestTTLGuard_compactBoundary(t *testing.T) {
	t.Run("compact 後に応答が無ければ通す (手動 /compact の直後)", func(t *testing.T) {
		f := newTTLGuardFixture(t)
		f.write(
			assistant(f.ago(61), "m1", 1000, 0),
			boundary(f.ago(60)),
		)
		f.assertPass("hello")
	})

	t.Run("compact 後の応答は通常どおり判定する", func(t *testing.T) {
		f := newTTLGuardFixture(t)
		f.write(
			assistant(f.ago(200), "m0", 1000, 0),
			boundary(f.ago(150)),
			assistant(f.ago(61), "m1", 1000, 0),
		)
		f.assertBlock("hello")
	})

	t.Run("compact 前の write の TTL は使わない", func(t *testing.T) {
		f := newTTLGuardFixture(t)
		f.write(
			assistant(f.ago(200), "m0", 1000, 0),
			boundary(f.ago(150)),
			assistant(f.ago(61), "m1", 0, 0),
		)
		f.assertPass("hello")
	})

	t.Run("sidechain の compact_boundary は無視する", func(t *testing.T) {
		f := newTTLGuardFixture(t)
		b := boundary(f.ago(60))
		b.Sidechain = true
		f.write(assistant(f.ago(61), "m1", 1000, 0), b)
		f.assertBlock("hello")
	})
}

func TestTTLGuard_input(t *testing.T) {
	t.Run("session_id が空なら通す", func(t *testing.T) {
		f := newTTLGuardFixture(t)
		f.write(assistant(f.ago(61), "m1", 1000, 0))
		w, err := f.s.TTLGuard(f.input("", "hello"), f.now)
		if err != nil || w != "" {
			t.Errorf("TTLGuard() = %q, %v, want 通す", w, err)
		}
	})

	t.Run("不正な session_id はエラー", func(t *testing.T) {
		f := newTTLGuardFixture(t)
		f.write(assistant(f.ago(61), "m1", 1000, 0))
		if _, err := f.s.TTLGuard(f.input("../etc", "hello"), f.now); err == nil {
			t.Error("エラーが返らなかった")
		}
	})

	t.Run("壊れた JSON はエラー", func(t *testing.T) {
		f := newTTLGuardFixture(t)
		if _, err := f.s.TTLGuard([]byte(`{"prompt": `), f.now); err == nil {
			t.Error("エラーが返らなかった")
		}
	})
}
