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
	"github.com/usadamasa/agents-daemon/internal/multiplexer"
	"github.com/usadamasa/agents-daemon/internal/multiplexer/multiplexerfake"
	"github.com/usadamasa/agents-daemon/internal/sessionstate"
)

var errFakeUnreachable = errors.New("fake: herdr unreachable")

// noEnv は環境変数が 1 つも無い getenv｡doctor の既存の項目は環境に依らない｡
func noEnv(string) string { return "" }

// envOf は m だけを持つ getenv を返す｡
func envOf(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestRunDoctor(t *testing.T) {
	fixedNow := time.Date(2026, 8, 16, 13, 0, 0, 0, time.UTC)

	t.Run("herdr に到達できないと error を返し､以降の項目は診断しない", func(t *testing.T) {
		client := &multiplexerfake.Client{
			ReachableFunc: func(context.Context) error { return errFakeUnreachable },
		}
		var buf bytes.Buffer
		err := RunDoctor(context.Background(), &buf, client, sessionstate.MultiplexerKindHerdr, apppath.New(t.TempDir(), "", ""), fixedNow, noEnv)
		if err == nil {
			t.Fatal("herdr 到達不可のとき error を期待したが nil だった")
		}
		if strings.Contains(buf.String(), "pane 一覧") {
			t.Errorf("到達不可の場合は pane 一覧以降を診断しないはずだが出力に含まれている: %s", buf.String())
		}
	})

	t.Run("herdr に到達できれば pane 一覧・state・設定・daemon・パスを報告して nil を返す", func(t *testing.T) {
		home := t.TempDir()
		client := &multiplexerfake.Client{
			ReachableFunc: func(context.Context) error { return nil },
			PaneListFunc: func(context.Context) ([]multiplexer.Pane, error) {
				return []multiplexer.Pane{
					{PaneID: "p1", Agent: "claude", AgentStatus: multiplexer.AgentStatusIdle, CWD: "/repo",
						SessionID: "session-abc"},
					{PaneID: "p2", Agent: "", AgentStatus: multiplexer.AgentStatusUnknown, CWD: "/other"},
				}, nil
			},
		}

		var buf bytes.Buffer
		if err := RunDoctor(context.Background(), &buf, client, sessionstate.MultiplexerKindHerdr, apppath.New(home, "", ""), fixedNow, noEnv); err != nil {
			t.Fatalf("RunDoctor() error = %v", err)
		}
		out := buf.String()

		for _, want := range []string{
			"backend: herdr (REX_SESSION なし)",
			"OK: herdr server は稼働しています",
			"pane=p1", "session_id=session-abc",
			"state ファイルはまだありません",
			"は存在しません｡全項目デフォルト値で動作します",
			"daemon は起動していません",
			"-- peer messaging",
			"CLAUDE_CODE_MESSAGING_SOCKET: (なし)",
			"config:      " + filepath.Join(home, ".config", "agents-daemon", "config.json"),
		} {
			if !strings.Contains(out, want) {
				t.Errorf("出力に %q が含まれていない｡出力:\n%s", want, out)
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
		client := &multiplexerfake.Client{
			ReachableFunc: func(context.Context) error { return nil },
			PaneListFunc: func(context.Context) ([]multiplexer.Pane, error) {
				return []multiplexer.Pane{{
					Agent:       "claude-code",
					AgentStatus: multiplexer.AgentStatusIdle,
					PaneID:      "p1",
					SessionID:   "sess-1",
				}}, nil
			},
		}

		var buf bytes.Buffer
		if err := RunDoctor(context.Background(), &buf, client, sessionstate.MultiplexerKindHerdr, p, fixedNow, noEnv); err != nil {
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
		client := &multiplexerfake.Client{
			ReachableFunc: func(context.Context) error { return nil },
			PaneListFunc:  func(context.Context) ([]multiplexer.Pane, error) { return nil, nil },
		}

		var buf bytes.Buffer
		if err := RunDoctor(context.Background(), &buf, client, sessionstate.MultiplexerKindHerdr, p, fixedNow, noEnv); err != nil {
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

	t.Run("rex なら backend と到達性を rex の名前で報告する", func(t *testing.T) {
		client := &multiplexerfake.Client{
			ReachableFunc: func(context.Context) error { return nil },
			PaneListFunc:  func(context.Context) ([]multiplexer.Pane, error) { return nil, nil },
		}
		var buf bytes.Buffer
		if err := RunDoctor(context.Background(), &buf, client, sessionstate.MultiplexerKindRex, apppath.New(t.TempDir(), "", ""), fixedNow, noEnv); err != nil {
			t.Fatalf("RunDoctor() error = %v", err)
		}
		for _, want := range []string{"backend: rex (REX_SESSION あり)", "OK: rex server は稼働しています"} {
			if !strings.Contains(buf.String(), want) {
				t.Errorf("出力に %q が無い:\n%s", want, buf.String())
			}
		}
	})

	t.Run("rex に到達できなければ rex の名前で error を返す", func(t *testing.T) {
		client := &multiplexerfake.Client{
			ReachableFunc: func(context.Context) error { return errFakeUnreachable },
		}
		var buf bytes.Buffer
		err := RunDoctor(context.Background(), &buf, client, sessionstate.MultiplexerKindRex, apppath.New(t.TempDir(), "", ""), fixedNow, noEnv)
		if err == nil || !strings.Contains(err.Error(), "rex に到達できない") {
			t.Fatalf("RunDoctor() error = %v, want rex に到達できない", err)
		}
	})

	t.Run("daemon の backend が今の環境と違えば警告する", func(t *testing.T) {
		cases := []struct {
			name        string
			snapBackend string
			snapPID     int
			wantWarn    bool
		}{
			{"herdr の daemon を rex の環境から見た", sessionstate.MultiplexerKindHerdr, os.Getpid(), true},
			{"同じ backend なら警告しない", sessionstate.MultiplexerKindRex, os.Getpid(), false},
			{"変更前の daemon (記録なし) は警告しない", "", os.Getpid(), false},
			{"前の daemon が残した snapshot は警告しない", sessionstate.MultiplexerKindHerdr, os.Getpid() + 1, false},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				p := apppath.New(t.TempDir(), "", "")
				writeFixturePIDFile(t, p.PIDFile(), os.Getpid())
				writeFixtureSnapshot(t, p.StatusFile(), daemon.StatusSnapshot{
					PID:       tc.snapPID,
					Backend:   tc.snapBackend,
					StartedAt: fixedNow.Add(-time.Hour),
					UpdatedAt: fixedNow,
				})
				client := &multiplexerfake.Client{
					ReachableFunc: func(context.Context) error { return nil },
					PaneListFunc:  func(context.Context) ([]multiplexer.Pane, error) { return nil, nil },
				}
				var buf bytes.Buffer
				if err := RunDoctor(context.Background(), &buf, client, sessionstate.MultiplexerKindRex, p, fixedNow, noEnv); err != nil {
					t.Fatalf("RunDoctor() error = %v (警告は exit code を変えない)", err)
				}
				warned := strings.Contains(buf.String(), "⚠ daemon の backend")
				if warned != tc.wantWarn {
					t.Errorf("警告の有無 = %v, want %v:\n%s", warned, tc.wantWarn, buf.String())
				}
			})
		}
	})
}

func TestReportPeerMessaging(t *testing.T) {
	// パスは全て t.TempDir() からの相対で書く｡実パス (/tmp/claude-501 等) を書かないのは､
	// sandbox 内の go test では t.TempDir() 自体が /tmp/claude-<uid> の下になり､fixture が環境と交差するため｡
	cases := []struct {
		name         string
		tmpdir       string // CLAUDE_CODE_TMPDIR (空なら未設定)
		socket       string // CLAUDE_CODE_MESSAGING_SOCKET (空なら未設定)
		socketExists bool
		want         []string
		wantNone     []string
	}{
		{
			name:     "環境変数が無ければ無効か外部起動と報告し OK/NG は出さない",
			want:     []string{"CLAUDE_CODE_MESSAGING_SOCKET: (なし)", "peer-messaging.md"},
			wantNone: []string{"OK: socket", "NG: socket"},
		},
		{
			name:         "socket があれば OK",
			socket:       "cc-socks/123.sock",
			socketExists: true,
			want:         []string{"OK: socket があります"},
			wantNone:     []string{"NG: socket", "⚠ socket が CLAUDE_CODE_TMPDIR"},
		},
		{
			name:     "環境変数はあるのに socket が無ければ NG",
			socket:   "cc-socks/missing.sock",
			want:     []string{"NG: socket がありません"},
			wantNone: []string{"OK: socket"},
		},
		{
			// 親セッションの Bash ツールにも CLAUDE_CODE_TMPDIR は来る (実測: /tmp/claude-501)｡
			// socket が /tmp/cc-socks/ に居る通常のセッションで警告してはいけない｡
			name:         "CLAUDE_CODE_TMPDIR があっても socket がその外なら警告しない",
			tmpdir:       "claude-501",
			socket:       "cc-socks/123.sock",
			socketExists: true,
			want:         []string{"OK: socket があります"},
			wantNone:     []string{"⚠ socket が CLAUDE_CODE_TMPDIR"},
		},
		{
			name:     "名前が接頭辞として一致するだけの別ディレクトリは配下と見なさない",
			tmpdir:   "claude-50",
			socket:   "claude-501/cc-socks/missing.sock",
			want:     []string{"NG: socket がありません"},
			wantNone: []string{"⚠ socket が CLAUDE_CODE_TMPDIR"},
		},
		{
			name:   "socket が CLAUDE_CODE_TMPDIR の下なら入れ子の起動として警告する",
			tmpdir: "claude-501",
			socket: "claude-501/cc-socks/missing.sock",
			want:   []string{"NG: socket がありません", "⚠ socket が CLAUDE_CODE_TMPDIR"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tempdir := t.TempDir()
			env := map[string]string{}
			if tc.tmpdir != "" {
				env["CLAUDE_CODE_TMPDIR"] = filepath.Join(tempdir, tc.tmpdir)
			}
			if tc.socket != "" {
				env["CLAUDE_CODE_MESSAGING_SOCKET"] = filepath.Join(tempdir, tc.socket)
			}
			if tc.socketExists {
				touchFile(t, env["CLAUDE_CODE_MESSAGING_SOCKET"])
			}
			var buf bytes.Buffer
			reportPeerMessaging(&buf, envOf(env))
			out := buf.String()
			for _, want := range tc.want {
				if !strings.Contains(out, want) {
					t.Errorf("出力に %q が無い:\n%s", want, out)
				}
			}
			for _, none := range tc.wantNone {
				if strings.Contains(out, none) {
					t.Errorf("出力に %q が出てはいけない:\n%s", none, out)
				}
			}
		})
	}
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
		t.Fatalf("pollIntervalSeconds の行が出力に無い｡出力:\n%s", buf.String())
	}
	if !strings.Contains(customLine, "42") || !strings.Contains(customLine, "(*)") {
		t.Errorf("カスタム値の差分マークが無い: %q", customLine)
	}

	defaultLine, ok := findLineContaining(buf.String(), "marginSeconds")
	if !ok {
		t.Fatalf("marginSeconds の行が出力に無い｡出力:\n%s", buf.String())
	}
	if strings.Contains(defaultLine, "(*)") {
		t.Errorf("デフォルト値のままの行に差分マークが付いている: %q", defaultLine)
	}
}

// touchFile は path に空ファイルを作る (親ディレクトリも作る)｡socket の存在だけを見る fixture 用｡
func touchFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}

// findLineContaining は out の中から substr を含む最初の行を返す｡
func findLineContaining(out, substr string) (string, bool) {
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, substr) {
			return line, true
		}
	}
	return "", false
}

// mustRateLimitFile は sessionID に対応する利用上限 state のパスを返す｡
// ファイル名の組み立ては sessionstate が唯一の実装元なので､テストもそこを通す｡
func mustRateLimitFile(t *testing.T, p apppath.Paths, sessionID string) string {
	t.Helper()
	path, err := sessionstate.New(p.StateDir()).RateLimitFile(sessionID)
	if err != nil {
		t.Fatalf("RateLimitFile(%q) error = %v", sessionID, err)
	}
	return path
}

// writeJSONFile はテスト用に path へ v を JSON として書き込む (親ディレクトリも作る)｡
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

// writeFixturePIDFile / writeFixtureSnapshot は doctor が読む状態を作る｡
//
// daemon 側の書き込み関数を呼ばないのは､それらを「テストのためだけに公開する」
// ことになり､daemon の API surface を実際の用途以上に広げてしまうため
// (analyze-modularity の high_public_ratio がこれを検出した)｡読み取りに使う
// StatusSnapshot 型は公開されているので､書式の知識はここへ漏れない｡
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
