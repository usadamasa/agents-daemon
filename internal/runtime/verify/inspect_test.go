package verify

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/usadamasa/agents-daemon/internal/apppath"
	"github.com/usadamasa/agents-daemon/internal/multiplexer"
	"github.com/usadamasa/agents-daemon/internal/multiplexer/multiplexerfake"
	"github.com/usadamasa/agents-daemon/internal/runtime/config"
)

// 2026-08-16 に実機 (Claude Code 2.1.224) で採取した通常のフッター。
const inspectSampleNormalScreen = `  Bash(rex capture -b "$REX_BLOCK" --trim --unwrap)

· Tinkering… (2m 52s · ↓ 9.0k tokens · thought for 8s)
  ⎿  Tip: Use /btw to ask a quick side question without interrupting Claude's current work
                                                                          105021 tokens
────────────────────────────────────────────────────────────────────────────
❯
────────────────────────────────────────────────────────────────────────────
  🌸 Opus 5 💎 ･ﾟ 💰 $1.60 ･ﾟ 🧠 104k (10%) ･ﾟ ⚡ high ･ﾟ 🎀 Mikawa ･ﾟ 🐣 v2.1.224
  ⏵⏵ auto mode on (shift+tab to cycle) · ← for agents`

// 2026-08-16 に実機で観測した上限到達画面。上限の文は画面にあるが、判定は transcript で決まる。
const inspectSampleLimitScreen = `  ⏺ Bash(task test)
  ⎿  ✅ テスト完了

⏺ You've hit your session limit · resets 12:30pm (Asia/Tokyo)
  /upgrade to increase your usage limit.

────────────────────────────────────────────────────────────────────────────
❯
────────────────────────────────────────────────────────────────────────────
  🌸 Opus 5 💎 ･ﾟ 💰 $22.10 ･ﾟ 🧠 229k (23%) ･ﾟ ⚡ high ･ﾟ 🎀 Mikawa ･ﾟ 🐣 v2.1.224
  📁 ~/src/github.com/usadamasa/claude-config ･ﾟ 🌿 feat/claude-auto-retry ✨
  ⏵⏵ auto mode on (shift+tab to cycle) · ← for agents
`

// 解除済みで Enter 待ちの通知欄。
const inspectSampleStaleScreen = "────────\n❯ \n────────\n  ⚠ Your usage limit has reset\n      Press enter to continue\n"

// transcript の末尾の entry (transcript/testdata と同じ形)。resetsAt は 2026-10-04T12:30:00Z。
const (
	inspectLimitedEntry = `{"type":"assistant","uuid":"u-limit","timestamp":"2026-10-04T08:32:13.884Z","message":{"role":"assistant","content":[{"type":"text","text":"You've hit your session limit · resets 9:30pm (Asia/Tokyo)"}]},"quotaLimits":{"status":"rejected","resetsAt":1791117000,"rateLimitType":"five_hour","overageStatus":"rejected"},"error":"rate_limit","isApiErrorMessage":true,"apiErrorStatus":429,"version":"2.1.289"}`
	inspectIdleEntry    = `{"type":"assistant","uuid":"u-done","timestamp":"2026-10-04T08:20:53.022Z","message":{"role":"assistant","content":[{"type":"text","text":"done"}]},"version":"2.1.289"}`
)

const inspectTestSessionID = "inspect-test-session"

// inspectEnv は Claude Code の設定ディレクトリ (transcript と登録簿) と XDG の home を一時ディレクトリに作る。
type inspectEnv struct {
	home      string
	claudeDir string
	paths     apppath.Paths
}

func newInspectEnv(t *testing.T) inspectEnv {
	t.Helper()
	home := t.TempDir()
	return inspectEnv{home: home, claudeDir: filepath.Join(home, ".claude"), paths: apppath.New(home, "", "")}
}

func (e inspectEnv) transcript(t *testing.T, sessionID, line string) {
	t.Helper()
	writeFile(t, filepath.Join(e.claudeDir, "projects", "-work", sessionID+".jsonl"), line+"\n")
}

func (e inspectEnv) registry(t *testing.T, pid int, sessionID string) {
	t.Helper()
	writeFile(t, filepath.Join(e.claudeDir, "sessions", "4242.json"), `{"pid":`+itoa(pid)+`,"sessionId":"`+sessionID+`","name":"peer"}`)
}

func itoa(n int) string {
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}

func (e inspectEnv) input(client PaneClient, paneID, file, sessionID string, now time.Time) InspectInput {
	return InspectInput{Client: client, Paths: e.paths, ClaudeConfigDir: e.claudeDir, PaneID: paneID, File: file, SessionID: sessionID, Now: now}
}

func TestRunInspect_判定はtranscriptで決まる(t *testing.T) {
	fixedNow := time.Date(2026, 10, 4, 9, 0, 0, 0, time.UTC)

	t.Run("上限の画面でも transcript が普通に終わっていれば none", func(t *testing.T) {
		env := newInspectEnv(t)
		file := writeInspectFixture(t, env.home, "limit.txt", inspectSampleLimitScreen)
		env.transcript(t, inspectTestSessionID, inspectIdleEntry)

		var buf bytes.Buffer
		if err := RunInspect(context.Background(), &buf, env.input(nil, "", file, inspectTestSessionID, fixedNow)); err != nil {
			t.Fatalf("RunInspect() error = %v", err)
		}
		out := buf.String()
		for _, want := range []string{"対象セッション: " + inspectTestSessionID + " (flag)", "entry: uuid=u-done type=assistant", "判定結果: none", "daemon は何もしません", "通知欄の判定: none"} {
			if !strings.Contains(out, want) {
				t.Errorf("出力に %q が無い:\n%s", want, out)
			}
		}
		if strings.Contains(out, "起床時刻の算出") {
			t.Errorf("none なのに起床時刻の算出が出ている:\n%s", out)
		}
	})

	t.Run("transcript が上限なら limit と判定し、resetsAt 由来の起床時刻を示す", func(t *testing.T) {
		env := newInspectEnv(t)
		file := writeInspectFixture(t, env.home, "normal.txt", inspectSampleNormalScreen)
		env.transcript(t, inspectTestSessionID, inspectLimitedEntry)

		var buf bytes.Buffer
		if err := RunInspect(context.Background(), &buf, env.input(nil, "", file, inspectTestSessionID, fixedNow)); err != nil {
			t.Fatalf("RunInspect() error = %v", err)
		}
		out := buf.String()
		// resetsAt は entry の epoch 秒から time.Unix で組み立てるので、表示はローカルの tz になる。
		resetsAt := time.Unix(1791117000, 0)
		for _, want := range []string{
			"error: rate_limit (status 429)",
			"quotaLimits: status=rejected rateLimitType=five_hour resetsAt=" + resetsAt.Format(time.RFC3339),
			"判定結果: limit",
			"出所: transcript の entry の resetsAt (未来) + margin 1m0s",
			"起床時刻: " + resetsAt.Add(time.Minute).Format(time.RFC3339),
			"→ 待機明けには esc → 文面 → enter を送ります",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("出力に %q が無い:\n%s", want, out)
			}
		}
	})

	t.Run("entry が 24 時間より古ければ待機に入らない旨を出す", func(t *testing.T) {
		env := newInspectEnv(t)
		file := writeInspectFixture(t, env.home, "normal.txt", inspectSampleNormalScreen)
		env.transcript(t, inspectTestSessionID, inspectLimitedEntry)

		rep, err := CollectInspect(context.Background(), env.input(nil, "", file, inspectTestSessionID, fixedNow.Add(48*time.Hour)))
		if err != nil {
			t.Fatalf("CollectInspect() error = %v", err)
		}
		if rep.Kind != "limit" || !rep.StaleTurn {
			t.Errorf("report = kind %s stale_turn %v, want limit / true", rep.Kind, rep.StaleTurn)
		}
		var buf bytes.Buffer
		writeInspect(&buf, rep)
		if !strings.Contains(buf.String(), "stale-turn") {
			t.Errorf("出力に stale-turn が無い:\n%s", buf.String())
		}
	})

	t.Run("transcript が無ければ理由を出して none", func(t *testing.T) {
		env := newInspectEnv(t)
		file := writeInspectFixture(t, env.home, "normal.txt", inspectSampleNormalScreen)

		var buf bytes.Buffer
		if err := RunInspect(context.Background(), &buf, env.input(nil, "", file, inspectTestSessionID, fixedNow)); err != nil {
			t.Fatalf("RunInspect() error = %v", err)
		}
		out := buf.String()
		if !strings.Contains(out, "読めない: transcript が見つからない") || !strings.Contains(out, "判定結果: none") {
			t.Errorf("出力:\n%s", out)
		}
	})

	t.Run("--file だけなら対象セッションが決まらない旨を出す", func(t *testing.T) {
		env := newInspectEnv(t)
		file := writeInspectFixture(t, env.home, "normal.txt", inspectSampleNormalScreen)

		var buf bytes.Buffer
		if err := RunInspect(context.Background(), &buf, env.input(nil, "", file, "", fixedNow)); err != nil {
			t.Fatalf("RunInspect() error = %v", err)
		}
		if out := buf.String(); !strings.Contains(out, "対象セッション: 決まらない") {
			t.Errorf("出力:\n%s", out)
		}
	})

	t.Run("Enter 待ちの通知欄は Enter だけを送ると出す", func(t *testing.T) {
		env := newInspectEnv(t)
		file := writeInspectFixture(t, env.home, "stale.txt", inspectSampleStaleScreen)
		env.transcript(t, inspectTestSessionID, inspectLimitedEntry)

		var buf bytes.Buffer
		if err := RunInspect(context.Background(), &buf, env.input(nil, "", file, inspectTestSessionID, fixedNow)); err != nil {
			t.Fatalf("RunInspect() error = %v", err)
		}
		if out := buf.String(); !strings.Contains(out, "通知欄の判定: resume-ready") || !strings.Contains(out, "Enter だけを送ります") {
			t.Errorf("出力:\n%s", out)
		}
	})
}

func TestRunInspect_pane(t *testing.T) {
	fixedNow := time.Date(2026, 10, 4, 9, 0, 0, 0, time.UTC)

	t.Run("--pane は画面を読み、pane の session から transcript を引く", func(t *testing.T) {
		env := newInspectEnv(t)
		env.transcript(t, inspectTestSessionID, inspectLimitedEntry)
		var gotOpts multiplexer.PaneReadOptions
		client := &multiplexerfake.Client{
			PaneReadFunc: func(_ context.Context, paneID string, opts multiplexer.PaneReadOptions) (string, error) {
				gotOpts = opts
				return inspectSampleLimitScreen, nil
			},
			PaneGetFunc: func(_ context.Context, paneID string) (multiplexer.Pane, error) {
				return multiplexer.Pane{PaneID: paneID, Agent: "claude", AgentStatus: multiplexer.AgentStatusBlocked,
					AgentKind: "permission", AgentMessage: "approve Bash: rm", SessionID: inspectTestSessionID}, nil
			},
		}

		var buf bytes.Buffer
		if err := RunInspect(context.Background(), &buf, env.input(client, "wH:p9", "", "", fixedNow)); err != nil {
			t.Fatalf("RunInspect() error = %v", err)
		}
		// PaneRead (画面) と PaneGet (record と session_id) の 1 回ずつ。
		if got, want := len(client.Calls), 2; got != want {
			t.Fatalf("Client への呼び出し回数 = %d, want %d (%v)", got, want, client.Calls)
		}
		if want := config.Default().ReadLines; gotOpts.Lines != want {
			t.Errorf("PaneReadOptions.Lines = %d, want %d (config.Default() の readLines)", gotOpts.Lines, want)
		}
		out := buf.String()
		for _, want := range []string{"agent_status=blocked", "kind=permission", `msg="approve Bash: rm"`, "対象セッション: " + inspectTestSessionID + " (statusline)", "判定結果: limit"} {
			if !strings.Contains(out, want) {
				t.Errorf("出力に %q が無い:\n%s", want, out)
			}
		}
	})

	t.Run("pane に session が無ければ前景の pid で登録簿から補う", func(t *testing.T) {
		env := newInspectEnv(t)
		env.registry(t, 4242, inspectTestSessionID)
		env.transcript(t, inspectTestSessionID, inspectLimitedEntry)
		client := &multiplexerfake.Client{
			PaneReadFunc: func(context.Context, string, multiplexer.PaneReadOptions) (string, error) {
				return inspectSampleNormalScreen, nil
			},
			PaneGetFunc: func(_ context.Context, paneID string) (multiplexer.Pane, error) {
				return multiplexer.Pane{PaneID: paneID, Agent: "claude", AgentStatus: multiplexer.AgentStatusError, PID: 4242}, nil
			},
		}

		rep, err := CollectInspect(context.Background(), env.input(client, "wH:p9", "", "", fixedNow))
		if err != nil {
			t.Fatalf("CollectInspect() error = %v", err)
		}
		if rep.SessionID != inspectTestSessionID || rep.SessionSource != "registry" || rep.Kind != "limit" || rep.Wake == nil {
			t.Errorf("report = %+v", rep)
		}
		if rep.Transcript.LastEntry == nil || rep.Transcript.LastEntry.Quota == nil || rep.Transcript.LastEntry.Quota.RateLimitType != "five_hour" {
			t.Errorf("transcript = %+v", rep.Transcript)
		}
	})

	t.Run("--pane 指定時は送信系メソッドを一切呼ばない", func(t *testing.T) {
		env := newInspectEnv(t)
		client := &multiplexerfake.Client{
			PaneReadFunc: func(context.Context, string, multiplexer.PaneReadOptions) (string, error) {
				return inspectSampleLimitScreen, nil
			},
		}
		var buf bytes.Buffer
		if err := RunInspect(context.Background(), &buf, env.input(client, "wH:p9", "", "", fixedNow)); err != nil {
			t.Fatalf("RunInspect() error = %v", err)
		}
		for _, call := range client.Calls {
			if strings.HasPrefix(call, "SendText") || strings.HasPrefix(call, "SendKeys") {
				t.Fatalf("inspect が送信系メソッドを呼んでしまっている: %v", client.Calls)
			}
		}
		if !strings.Contains(buf.String(), "pane の情報を取れません") {
			t.Errorf("PaneGet の失敗を報告していない:\n%s", buf.String())
		}
	})
}

func writeInspectFixture(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	return path
}
