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
	"github.com/usadamasa/agents-daemon/internal/config"
	"github.com/usadamasa/agents-daemon/internal/herdrcli"
	"github.com/usadamasa/agents-daemon/internal/herdrcli/herdrclifake"
)

// 2026-08-16 に実機 (Claude Code 2.1.224) で採取した通常のフッター｡
// internal/detect のテストと同じ現物 (誤検知しないことの確認が主目的)｡
const inspectSampleNormalScreen = `  Bash(herdr pane read "$HERDR_PANE_ID" --source detection --lines 12)

· Tinkering… (2m 52s · ↓ 9.0k tokens · thought for 8s)
  ⎿  Tip: Use /btw to ask a quick side question without interrupting Claude's current work
                                                                          105021 tokens
────────────────────────────────────────────────────────────────────────────
❯
────────────────────────────────────────────────────────────────────────────
  🌸 Opus 5 💎 ･ﾟ 💰 $1.60 ･ﾟ 🧠 104k (10%) ･ﾟ ⚡ high ･ﾟ 🎀 Mikawa ･ﾟ 🐣 v2.1.224
  ⏵⏵ auto mode on (shift+tab to cycle) · ← for agents`

// 2026-08-16 に実機で観測した実際の上限到達画面｡internal/detect/real_sample_test.go
// の realLimitScreen と同じ現物｡
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

// inspectTestSessionID は --file 経由のテストで利用上限 state を引くための
// ダミーのセッション ID｡
const inspectTestSessionID = "inspect-test-session"

func TestRunInspect_サンプル画面での分類(t *testing.T) {
	fixedNow := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)

	t.Run("通常のフッターは none と判定し送信ロジックへ進まない", func(t *testing.T) {
		home := t.TempDir()
		file := writeInspectFixture(t, home, "normal.txt", inspectSampleNormalScreen)

		var buf bytes.Buffer
		if err := RunInspect(context.Background(), &buf, &herdrclifake.Client{}, apppath.New(home, "", ""), "", file, "", fixedNow); err != nil {
			t.Fatalf("RunInspect() error = %v", err)
		}
		out := buf.String()
		if !strings.Contains(out, "判定結果: none") {
			t.Errorf("none 判定の報告が無い｡出力:\n%s", out)
		}
		if strings.Contains(out, "起床時刻の算出") {
			t.Errorf("none 判定なのに起床時刻の算出が出力されている: %s", out)
		}
	})

	t.Run("実機の上限画面は limit と判定し起床時刻の根拠を示す", func(t *testing.T) {
		home := t.TempDir()
		file := writeInspectFixture(t, home, "limit.txt", inspectSampleLimitScreen)

		var buf bytes.Buffer
		if err := RunInspect(context.Background(), &buf, &herdrclifake.Client{}, apppath.New(home, "", ""), "", file, "", fixedNow); err != nil {
			t.Fatalf("RunInspect() error = %v", err)
		}
		out := buf.String()
		if !strings.Contains(out, "判定結果: limit") {
			t.Errorf("limit 判定の報告が無い｡出力:\n%s", out)
		}
		if !strings.Contains(out, "起床時刻の算出") {
			t.Errorf("limit 判定なのに起床時刻の算出が出力されていない: %s", out)
		}
		if !strings.Contains(out, "state ファイルが無いため､ゲートは働きません") {
			t.Errorf("state 無しのゲート報告が無い: %s", out)
		}
		// state が無くても､実機の画面には絶対時刻 (12:30pm (Asia/Tokyo)) があるので
		// そこから起床時刻を決める｡fixedNow (21:00 JST) では既に過ぎているため
		// 解除済み扱いになる｡fallback (5 時間待ち) へは落ちない｡
		if !strings.Contains(out, "出所: 画面の絶対時刻 (既に経過") {
			t.Errorf("画面の絶対時刻由来の起床時刻の報告が無い: %s", out)
		}
		if strings.Contains(out, "フォールバック") {
			t.Errorf("画面に絶対時刻があるのに fallback へ落ちている: %s", out)
		}
	})

	t.Run("fresh な state があれば resets_at 由来の起床時刻を優先する", func(t *testing.T) {
		home := t.TempDir()
		file := writeInspectFixture(t, home, "limit.txt", inspectSampleLimitScreen)
		p := apppath.New(home, "", "")
		writeJSONFile(t, mustRateLimitFile(t, p, inspectTestSessionID), map[string]any{
			"five_hour":   map[string]any{"used_percentage": 95, "resets_at": fixedNow.Add(30 * time.Minute).Unix()},
			"observed_at": fixedNow.Add(-time.Minute).Unix(),
		})

		var buf bytes.Buffer
		if err := RunInspect(context.Background(), &buf, &herdrclifake.Client{}, p, "", file, inspectTestSessionID, fixedNow); err != nil {
			t.Fatalf("RunInspect() error = %v", err)
		}
		out := buf.String()
		if !strings.Contains(out, "出所: 自セッションの statusline の resets_at") {
			t.Errorf("resets_at 由来の起床時刻の報告が無い: %s", out)
		}
		if !strings.Contains(out, "閾値以上: limit 検知時にゲートで抑制しません") {
			t.Errorf("閾値以上のゲート報告が無い: %s", out)
		}
	})

	t.Run("fresh な state が閾値未満ならゲートで抑制される旨を報告する", func(t *testing.T) {
		home := t.TempDir()
		file := writeInspectFixture(t, home, "limit.txt", inspectSampleLimitScreen)
		p := apppath.New(home, "", "")
		writeJSONFile(t, mustRateLimitFile(t, p, inspectTestSessionID), map[string]any{
			"five_hour":   map[string]any{"used_percentage": 10, "resets_at": fixedNow.Add(30 * time.Minute).Unix()},
			"observed_at": fixedNow.Add(-time.Minute).Unix(),
		})

		var buf bytes.Buffer
		if err := RunInspect(context.Background(), &buf, &herdrclifake.Client{}, p, "", file, inspectTestSessionID, fixedNow); err != nil {
			t.Fatalf("RunInspect() error = %v", err)
		}
		out := buf.String()
		if !strings.Contains(out, "閾値未満: limit と判定されても fresh な間はゲートで抑制されます") {
			t.Errorf("閾値未満のゲート抑制報告が無い: %s", out)
		}
	})

	t.Run("--pane 指定時は herdrcli.Client 経由で読み取る", func(t *testing.T) {
		home := t.TempDir()
		var gotOpts herdrcli.PaneReadOptions
		client := &herdrclifake.Client{
			PaneReadFunc: func(_ context.Context, paneID string, opts herdrcli.PaneReadOptions) (string, error) {
				gotOpts = opts
				return inspectSampleLimitScreen, nil
			},
		}

		var buf bytes.Buffer
		if err := RunInspect(context.Background(), &buf, client, apppath.New(home, "", ""), "wH:p9", "", "", fixedNow); err != nil {
			t.Fatalf("RunInspect() error = %v", err)
		}
		// PaneRead (画面) と PaneGet (どのセッションの利用上限 state を引くか) の 2 回｡
		// state はセッションごとに別ファイルなので､pane→session の解決が要る｡
		if got, want := len(client.Calls), 2; got != want {
			t.Fatalf("Client への呼び出し回数 = %d, want %d (%v)", got, want, client.Calls)
		}
		// 期待値は設定の既定から引く｡読み取り元の既定は viewport 制限を避けるために
		// 変わりうるので､ここに値を直書きすると既定を変えるたびに嘘になる｡
		wantSource := herdrcli.PaneReadSource(config.Default().ReadSource)
		if gotOpts.Source != wantSource {
			t.Errorf("PaneReadOptions.Source = %v, want %v (config.Default() の readSource)", gotOpts.Source, wantSource)
		}
		if !strings.Contains(buf.String(), "判定結果: limit") {
			t.Errorf("pane 読み取り結果が分類に反映されていない: %s", buf.String())
		}
	})

	t.Run("--pane 指定時は送信系メソッドを一切呼ばない", func(t *testing.T) {
		home := t.TempDir()
		client := &herdrclifake.Client{
			PaneReadFunc: func(context.Context, string, herdrcli.PaneReadOptions) (string, error) {
				return inspectSampleLimitScreen, nil
			},
		}
		var buf bytes.Buffer
		if err := RunInspect(context.Background(), &buf, client, apppath.New(home, "", ""), "wH:p9", "", "", fixedNow); err != nil {
			t.Fatalf("RunInspect() error = %v", err)
		}
		for _, call := range client.Calls {
			if strings.HasPrefix(call, "SendText") || strings.HasPrefix(call, "SendKeys") || strings.HasPrefix(call, "ReportMetadata") {
				t.Fatalf("inspect が送信系メソッドを呼んでしまっている: %v", client.Calls)
			}
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
