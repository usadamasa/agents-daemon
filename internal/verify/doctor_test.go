package verify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/usadamasa/agents-daemon/internal/apppath"
	"github.com/usadamasa/agents-daemon/internal/daemon"
	"github.com/usadamasa/agents-daemon/internal/herdrcli"
	"github.com/usadamasa/agents-daemon/internal/herdrcli/herdrclifake"
	"github.com/usadamasa/agents-daemon/internal/sessionstate"
)

var errFakeUnreachable = errors.New("fake: herdr unreachable")

func TestRunDoctor(t *testing.T) {
	fixedNow := time.Date(2026, 8, 16, 13, 0, 0, 0, time.UTC)

	t.Run("herdr に到達できないと error を返し、以降の項目は診断しない", func(t *testing.T) {
		client := &herdrclifake.Client{
			ReachableFunc: func(context.Context) error { return errFakeUnreachable },
		}
		var buf bytes.Buffer
		err := RunDoctor(context.Background(), &buf, client, apppath.New(t.TempDir(), "", ""), fixedNow)
		if err == nil {
			t.Fatal("herdr 到達不可のとき error を期待したが nil だった")
		}
		if strings.Contains(buf.String(), "pane 一覧") {
			t.Errorf("到達不可の場合は pane 一覧以降を診断しないはずだが出力に含まれている: %s", buf.String())
		}
	})

	t.Run("herdr に到達できれば pane 一覧・state・設定・daemon・パスを報告して nil を返す", func(t *testing.T) {
		home := t.TempDir()
		client := &herdrclifake.Client{
			ReachableFunc: func(context.Context) error { return nil },
			PaneListFunc: func(context.Context) ([]herdrcli.Pane, error) {
				return []herdrcli.Pane{
					{PaneID: "p1", Agent: "claude", AgentStatus: herdrcli.AgentStatusIdle, CWD: "/repo",
						AgentSession: &herdrcli.AgentSession{Value: "session-abc"}},
					{PaneID: "p2", Agent: "", AgentStatus: herdrcli.AgentStatusUnknown, CWD: "/other"},
				}, nil
			},
		}

		var buf bytes.Buffer
		if err := RunDoctor(context.Background(), &buf, client, apppath.New(home, "", ""), fixedNow); err != nil {
			t.Fatalf("RunDoctor() error = %v", err)
		}
		out := buf.String()

		for _, want := range []string{
			"OK: herdr server は稼働しています",
			"pane=p1", "session_id=session-abc",
			"state ファイルはまだありません",
			"は存在しません。全項目デフォルト値で動作します",
			"daemon は起動していません",
			"config:      " + filepath.Join(home, ".config", "agents-daemon", "config.json"),
		} {
			if !strings.Contains(out, want) {
				t.Errorf("出力に %q が含まれていない。出力:\n%s", want, out)
			}
		}
		if strings.Contains(out, "pane=p2") {
			t.Errorf("Claude agent でない pane を列挙してしまっている: %s", out)
		}
	})

	t.Run("rate-limits.json が新鮮で閾値以上なら発火抑制しない旨を報告する", func(t *testing.T) {
		home := t.TempDir()
		p := apppath.New(home, "", "")
		writeJSONFile(t, mustRateLimitFile(t, p, "sess-1"), map[string]any{
			"five_hour":   map[string]any{"used_percentage": 95, "resets_at": fixedNow.Add(time.Hour).Unix()},
			"observed_at": fixedNow.Add(-time.Minute).Unix(),
			"session_id":  "sess-1",
		})
		client := &herdrclifake.Client{
			ReachableFunc: func(context.Context) error { return nil },
			PaneListFunc: func(context.Context) ([]herdrcli.Pane, error) {
				return []herdrcli.Pane{{
					Agent:        "claude-code",
					AgentStatus:  herdrcli.AgentStatusIdle,
					PaneID:       "p1",
					AgentSession: &herdrcli.AgentSession{Value: "sess-1"},
				}}, nil
			},
		}

		var buf bytes.Buffer
		if err := RunDoctor(context.Background(), &buf, client, p, fixedNow); err != nil {
			t.Fatalf("RunDoctor() error = %v", err)
		}
		out := buf.String()
		if !strings.Contains(out, "鮮度: OK") {
			t.Errorf("鮮度 OK の報告が無い: %s", out)
		}
		if !strings.Contains(out, "閾値以上: limit 検知時にゲートで抑制しません") {
			t.Errorf("閾値以上の報告が無い: %s", out)
		}
	})

	t.Run("daemon が起動していれば pid と status snapshot を報告する", func(t *testing.T) {
		home := t.TempDir()
		p := apppath.New(home, "", "")
		writeFixturePIDFile(t, p.PIDFile(), os.Getpid())
		writeFixtureSnapshot(t, p.StatusFile(), daemon.StatusSnapshot{
			PID:       os.Getpid(),
			StartedAt: fixedNow.Add(-time.Hour),
			UpdatedAt: fixedNow,
		})
		client := &herdrclifake.Client{
			ReachableFunc: func(context.Context) error { return nil },
			PaneListFunc:  func(context.Context) ([]herdrcli.Pane, error) { return nil, nil },
		}

		var buf bytes.Buffer
		if err := RunDoctor(context.Background(), &buf, client, p, fixedNow); err != nil {
			t.Fatalf("RunDoctor() error = %v", err)
		}
		out := buf.String()
		if !strings.Contains(out, "daemon 起動中") {
			t.Errorf("daemon 起動中の報告が無い: %s", out)
		}
		if !strings.Contains(out, "監視中 pane: 0 件") {
			t.Errorf("監視中 pane 数の報告が無い: %s", out)
		}
	})
}

func TestReportConfigState_不正なJSONはフォールバックを明言する(t *testing.T) {
	home := t.TempDir()
	p := apppath.New(home, "", "")
	if err := os.MkdirAll(filepath.Dir(p.ConfigFile()), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(p.ConfigFile(), []byte("{not valid json"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	var buf bytes.Buffer
	reportConfigState(&buf, p)
	out := buf.String()
	if !strings.Contains(out, "JSON として不正です") {
		t.Errorf("不正 JSON の警告が無い: %s", out)
	}
}

func TestReportConfigState_カスタム値はデフォルトと異なる印がつく(t *testing.T) {
	home := t.TempDir()
	p := apppath.New(home, "", "")
	writeJSONFile(t, p.ConfigFile(), map[string]any{
		"pollIntervalSeconds": 42,
	})

	var buf bytes.Buffer
	reportConfigState(&buf, p)

	customLine, ok := findLineContaining(buf.String(), "pollIntervalSeconds")
	if !ok {
		t.Fatalf("pollIntervalSeconds の行が出力に無い。出力:\n%s", buf.String())
	}
	if !strings.Contains(customLine, "42") || !strings.Contains(customLine, "(*)") {
		t.Errorf("カスタム値の差分マークが無い: %q", customLine)
	}

	defaultLine, ok := findLineContaining(buf.String(), "marginSeconds")
	if !ok {
		t.Fatalf("marginSeconds の行が出力に無い。出力:\n%s", buf.String())
	}
	if strings.Contains(defaultLine, "(*)") {
		t.Errorf("デフォルト値のままの行に差分マークが付いている: %q", defaultLine)
	}
}

// findLineContaining は out の中から substr を含む最初の行を返す。
func findLineContaining(out, substr string) (string, bool) {
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, substr) {
			return line, true
		}
	}
	return "", false
}

// mustRateLimitFile は sessionID に対応する利用上限 state のパスを返す。
// ファイル名の組み立ては sessionstate が唯一の実装元なので、テストもそこを通す。
func mustRateLimitFile(t *testing.T, p apppath.Paths, sessionID string) string {
	t.Helper()
	path, err := sessionstate.New(p.StateDir()).RateLimitFile(sessionID)
	if err != nil {
		t.Fatalf("RateLimitFile(%q) error = %v", sessionID, err)
	}
	return path
}

// writeJSONFile はテスト用に path へ v を JSON として書き込む (親ディレクトリも作る)。
func writeJSONFile(t *testing.T, path string, v any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}

// writeFixturePIDFile / writeFixtureSnapshot は doctor が読む状態を作る。
//
// daemon 側の書き込み関数を呼ばないのは、それらを「テストのためだけに公開する」
// ことになり、daemon の API surface を実際の用途以上に広げてしまうため
// (analyze-modularity の high_public_ratio がこれを検出した)。読み取りに使う
// StatusSnapshot 型は公開されているので、書式の知識はここへ漏れない。
func writeFixturePIDFile(t *testing.T, path string, pid int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("state ディレクトリの作成に失敗: %v", err)
	}
	if err := os.WriteFile(path, []byte(strconv.Itoa(pid)+"\n"), 0o600); err != nil {
		t.Fatalf("PID ファイルの作成に失敗: %v", err)
	}
}

func writeFixtureSnapshot(t *testing.T, path string, snap daemon.StatusSnapshot) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("state ディレクトリの作成に失敗: %v", err)
	}
	body, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("status snapshot の marshal に失敗: %v", err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("status snapshot の作成に失敗: %v", err)
	}
}
