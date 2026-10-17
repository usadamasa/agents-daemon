package verify

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/usadamasa/agents-daemon/internal/apppath"
	"github.com/usadamasa/agents-daemon/internal/backend"
	"github.com/usadamasa/agents-daemon/internal/config"
	"github.com/usadamasa/agents-daemon/internal/detect"
	"github.com/usadamasa/agents-daemon/internal/monitor"
	"github.com/usadamasa/agents-daemon/internal/multiplexer"
	"github.com/usadamasa/agents-daemon/internal/sessionstate"
)

// simulateMetadataSource は simulate が使い捨て block に付ける label｡利用者が付けた label と
// 見分けがつくよう､ツール名を含めた固定値にする｡
const simulateMetadataSource = "agents-daemon-simulate"

// defaultSimulateScreen は --screen-file を省略した場合に使う内蔵の limit 画面｡
// 2026-08-16 に実機 (Claude Code 2.1.224) で観測した文面と同じ形
// (internal/detect/real_sample_test.go の realLimitScreen 参照)｡
const defaultSimulateScreen = `  ⏺ Bash(task test)
  ⎿  ✅ テスト完了

⏺ You've hit your session limit · resets 12:30pm (Asia/Tokyo)
  /upgrade to increase your usage limit.

────────────────────────────────────────────────────────────────────────────
❯
────────────────────────────────────────────────────────────────────────────
`

// SimulateOptions は RunSimulate の呼び出しオプション｡
type SimulateOptions struct {
	Send       bool
	Keep       bool
	ScreenFile string
}

// RunSimulate は simulate の本体｡使い捨て pane の作成から後始末まで一貫して行う｡
// backend は daemon と同じ規則 (REX_SESSION があること) で選ぶ｡
func RunSimulate(ctx context.Context, w io.Writer, p apppath.Paths, opts SimulateOptions) error {
	kind, execClient, err := backend.Connect(os.Getenv, sessionstate.New(p.StateDir()))
	if err != nil {
		return err
	}
	host, err := newSimulateHost(kind, os.Getenv)
	if err != nil {
		return err
	}
	if err := execClient.Reachable(ctx); err != nil {
		return fmt.Errorf("%s に到達できません: %w", kind, err)
	}

	screenContent := defaultSimulateScreen
	if opts.ScreenFile != "" {
		data, readErr := os.ReadFile(opts.ScreenFile) // #nosec G304 -- CLI 引数で明示指定
		if readErr != nil {
			return fmt.Errorf("--screen-file の読み込みに失敗: %w", readErr)
		}
		screenContent = string(data)
	}
	scriptPath, screenPath, removeFiles, err := writeSimulateFiles(simulateScript(host.ProgramStatusCommand()), screenContent)
	if err != nil {
		return err
	}
	// script は sh が読みながら実行し､screen は script が起動直後に cat する｡どちらも開いた
	// fd があれば削除後も読めるので､pane の後始末と一緒に消してよい｡
	defer removeFiles()

	_, _ = fmt.Fprintf(w, "使い捨て pane を作成します (backend=%s)...\n", kind)
	ownedPaneID, err := host.SplitPane(ctx, scriptPath, screenPath)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(w, "  pane %s を作成しました (前景は /bin/sh %s)\n", ownedPaneID, scriptPath)
	defer cleanupSimulatePane(ctx, w, host, ownedPaneID, opts.Keep)

	marked, err := host.MarkAsClaudeAgent(ctx, ownedPaneID)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(w, "  pane %s: %s\n", ownedPaneID, marked)
	// 登録が backend に載ったことを､daemon と同じ経路 (PaneGet → IsClaudeAgent) で確かめる｡
	// rex では script が OSC 7501 を出して record に載るまでの時間が挟まる｡
	pane, err := waitForClaudeAgent(ctx, execClient, ownedPaneID, markTimeout)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(w, "  Claude agent として見えました (agent=%s agent_status=%s)\n", pane.Agent, pane.AgentStatus)

	// script の cat が画面に出るのを待つ｡コマンドの投入はレンダリング完了を待たずに戻る
	// (実測: 固定 500ms 待機では画面反映前に読んでしまい none 判定になることがあった)｡
	if marker := lastNonEmptyLine(screenContent); marker != "" {
		if err := waitForScreenContent(ctx, execClient, ownedPaneID, marker, writeScreenTimeout); err != nil {
			return err
		}
	}
	_, _ = fmt.Fprintln(w, "  limit 風の画面が表示されました")

	return runSimulateTicks(ctx, w, p, ownedPaneID, execClient, opts)
}

// writeSimulateFiles は script と画面テキストを一時ディレクトリに書き､そのパスと削除関数を返す｡
func writeSimulateFiles(script, screen string) (scriptPath, screenPath string, remove func(), err error) {
	dir, err := os.MkdirTemp("", "agents-daemon-simulate-")
	if err != nil {
		return "", "", nil, fmt.Errorf("一時ディレクトリの作成に失敗: %w", err)
	}
	remove = func() { _ = os.RemoveAll(dir) }
	scriptPath = filepath.Join(dir, "simulate.sh")
	screenPath = filepath.Join(dir, "screen.txt")
	if err := os.WriteFile(scriptPath, []byte(script), 0o600); err != nil {
		remove()
		return "", "", nil, fmt.Errorf("script の書き込みに失敗: %w", err)
	}
	if err := os.WriteFile(screenPath, []byte(screen), 0o600); err != nil { // #nosec G703 -- screenPath は MkdirTemp のディレクトリ配下の固定名｡screen の中身は --screen-file の内容で､パスには使わない
		remove()
		return "", "", nil, fmt.Errorf("画面テキストの書き込みに失敗: %w", err)
	}
	return scriptPath, screenPath, remove, nil
}

// writeScreenTimeout は画面への反映を待つ最大時間｡
const writeScreenTimeout = 5 * time.Second

// markTimeout は Claude agent の登録が backend に載るのを待つ最大時間｡
const markTimeout = 5 * time.Second

// waitForClaudeAgent は paneID が multiplexer.IsClaudeAgent を満たすまで PaneGet をポーリングし､
// 満たした時点の Pane を返す｡
func waitForClaudeAgent(ctx context.Context, client multiplexer.Client, paneID string, timeout time.Duration) (multiplexer.Pane, error) {
	deadline := time.Now().Add(timeout)
	var last multiplexer.Pane
	var lastErr error
	for {
		pane, err := client.PaneGet(ctx, paneID)
		if err == nil {
			last = pane
			lastErr = nil
			if multiplexer.IsClaudeAgent(pane) {
				return pane, nil
			}
		} else {
			lastErr = err
		}
		if time.Now().After(deadline) {
			if lastErr != nil {
				return multiplexer.Pane{}, fmt.Errorf("pane %s が %s 以内に Claude agent として見えませんでした (最後の PaneGet の失敗: %w)", paneID, timeout, lastErr)
			}
			return multiplexer.Pane{}, fmt.Errorf("pane %s が %s 以内に Claude agent として見えませんでした (最後の観測: agent=%q agent_status=%s)", paneID, timeout, last.Agent, last.AgentStatus)
		}
		time.Sleep(150 * time.Millisecond)
	}
}

// lastNonEmptyLine は s の最後の空白以外を含む行を trim して返す｡--source detection は
// pane の viewport に収まる範囲しか見せない (実測)｡中身が viewport より長いと先頭の行から
// 順にスクロールアウトするため､待ち受けの目印には終端側の行を使う方が生き残りやすい｡
func lastNonEmptyLine(s string) string {
	lines := strings.Split(s, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if trimmed := strings.TrimSpace(lines[i]); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// waitForScreenContent は paneID の画面に substr が現れるまでポーリングする｡
func waitForScreenContent(ctx context.Context, client multiplexer.Client, paneID, substr string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastScreen string
	for {
		screen, err := client.PaneRead(ctx, paneID, multiplexer.PaneReadOptions{Lines: 40})
		if err == nil {
			lastScreen = screen
			if strings.Contains(screen, substr) {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("pane %s の画面に %q が %s 以内に反映されませんでした (最後に読めた内容: %q)", paneID, substr, timeout, lastScreen)
		}
		time.Sleep(150 * time.Millisecond)
	}
}

// cleanupSimulatePane は使い捨て pane の後始末をする｡ownedPaneID は必ず
// host.SplitPane がこのプロセス自身で作った値であり､呼び出し元にそれ以外の
// pane_id を渡す手段は無い｡失敗しても simulate 全体は失敗させず､手動での
// 後始末コマンドを案内するだけに留める｡
func cleanupSimulatePane(ctx context.Context, w io.Writer, host simulateHost, ownedPaneID string, keep bool) {
	if keep {
		_, _ = fmt.Fprintf(w, "--keep が指定されたため pane %s は残しています (`%s` で手動削除してください)\n", ownedPaneID, host.CloseCommand(ownedPaneID))
		return
	}
	if err := host.ClosePane(ctx, ownedPaneID); err != nil {
		_, _ = fmt.Fprintf(w, "後始末に失敗しました (`%s` で手動削除してください): %v\n", host.CloseCommand(ownedPaneID), err)
		return
	}
	_, _ = fmt.Fprintf(w, "pane %s を閉じました\n", ownedPaneID)
}

// runSimulateTicks は monitor.Tick を daemon と同じ設定で実行し､結果を w へ表示する｡
// 1 回目の Tick で limit を検知して待機状態に入った場合､2 回目は起床時刻を過ぎたと
// 仮定した合成の now を渡して再送信の経路 (recoverPane 経由の SendText/SendKeys) まで
// 確認する｡実時間では待たない (上限解除まで数時間かかるため)｡
func runSimulateTicks(ctx context.Context, w io.Writer, p apppath.Paths, ownedPaneID string, execClient multiplexer.Client, opts SimulateOptions) error {
	cfg, cfgErr := config.Load(p.ConfigFile())
	if cfgErr != nil {
		_, _ = fmt.Fprintf(w, "設定の読み込みに失敗､デフォルト値で継続します: %v\n", cfgErr)
		cfg = config.Default()
	}
	classifier, err := detect.NewClassifier(cfg.CustomPatterns)
	if err != nil {
		return fmt.Errorf("分類器の構築に失敗: %w", err)
	}
	store := sessionstate.New(p.StateDir())
	rateState, stateErr := store.LoadRateLimit(paneSessionID(ctx, execClient, ownedPaneID))
	if stateErr != nil {
		_, _ = fmt.Fprintf(w, "利用上限 state の読み込みに失敗､state 無しとして扱います: %v\n", stateErr)
		rateState = nil
	}
	account, accountErr := store.LatestWindow(time.Now())
	if accountErr != nil {
		_, _ = fmt.Fprintf(w, "アカウント全体の利用上限 state の走査に失敗､無しとして扱います: %v\n", accountErr)
		account = nil
	}

	guarded := newScopedClient(execClient, ownedPaneID)
	var tickClient multiplexer.Client = guarded
	mode := "dry-run (--send を付けると実際に送信します)"
	if opts.Send {
		mode = "実送信 (使い捨て pane に対してのみ)"
	} else {
		tickClient = simulateDryRunClient{inner: guarded, w: w}
	}
	_, _ = fmt.Fprintf(w, "\nmonitor.Tick を daemon と同じロジックで実行します (%s)\n", mode)

	pane, err := guarded.PaneGet(ctx, ownedPaneID)
	if err != nil {
		return fmt.Errorf("pane %s の取得に失敗: %w", ownedPaneID, err)
	}
	_, _ = fmt.Fprintf(w, "  観測した pane: agent=%s agent_status=%s\n", pane.Agent, pane.AgentStatus)

	deps := monitor.Deps{Client: tickClient, Classifier: classifier, Config: cfg, Sleep: time.Sleep, Account: account}
	ps := &monitor.PaneState{}

	outcome, tickErr := monitor.Tick(ctx, deps, pane, ps, rateState, time.Now())
	_, _ = fmt.Fprintf(w, "  1 回目の Tick → Outcome: %s\n", outcome)
	if tickErr != nil {
		_, _ = fmt.Fprintf(w, "    エラー: %v\n", tickErr)
	}

	if outcome != monitor.OutcomeWaiting {
		return nil
	}
	_, _ = fmt.Fprintf(w, "  待機状態に入りました (起床時刻 %s)\n", ps.WaitUntil.Format(time.RFC3339))

	synthNow := ps.WaitUntil.Add(time.Second)
	_, _ = fmt.Fprintf(w, "  2 回目の Tick は起床時刻を過ぎたと仮定して発火させます (実時間は待ちません, now=%s)\n", synthNow.Format(time.RFC3339))
	outcome2, tickErr2 := monitor.Tick(ctx, deps, pane, ps, rateState, synthNow)
	_, _ = fmt.Fprintf(w, "  2 回目の Tick → Outcome: %s\n", outcome2)
	if tickErr2 != nil {
		_, _ = fmt.Fprintf(w, "    エラー: %v\n", tickErr2)
	}
	return nil
}

// scopedClient は multiplexer.Client を包み､ownedPaneID 以外の pane を対象にした呼び出しを
// 全て拒否する｡simulate は自分が作った使い捨て pane 以外に絶対に触れてはいけないので､
// 「呼び出し側が正しい pane_id を渡す」という規律に頼るのではなく､型システムの外側で
// 機械的に強制できるガードとして実装する｡
type scopedClient struct {
	inner       multiplexer.Client
	ownedPaneID string
}

var errPaneNotOwned = errors.New("simulate が作成した pane 以外への操作を拒否しました")

func newScopedClient(inner multiplexer.Client, ownedPaneID string) *scopedClient {
	return &scopedClient{inner: inner, ownedPaneID: ownedPaneID}
}

func (c *scopedClient) guard(paneID string) error {
	if paneID != c.ownedPaneID {
		return fmt.Errorf("%w (要求された pane=%q, 所有 pane=%q)", errPaneNotOwned, paneID, c.ownedPaneID)
	}
	return nil
}

func (c *scopedClient) PaneList(ctx context.Context) ([]multiplexer.Pane, error) {
	return c.inner.PaneList(ctx) // 読み取り専用の一覧なので許可する
}

func (c *scopedClient) PaneGet(ctx context.Context, paneID string) (multiplexer.Pane, error) {
	if err := c.guard(paneID); err != nil {
		return multiplexer.Pane{}, err
	}
	return c.inner.PaneGet(ctx, paneID)
}

func (c *scopedClient) PaneRead(ctx context.Context, paneID string, opts multiplexer.PaneReadOptions) (string, error) {
	if err := c.guard(paneID); err != nil {
		return "", err
	}
	return c.inner.PaneRead(ctx, paneID, opts)
}

func (c *scopedClient) SendText(ctx context.Context, paneID, text string) error {
	if err := c.guard(paneID); err != nil {
		return err
	}
	return c.inner.SendText(ctx, paneID, text)
}

func (c *scopedClient) SendKeys(ctx context.Context, paneID string, keys ...string) error {
	if err := c.guard(paneID); err != nil {
		return err
	}
	return c.inner.SendKeys(ctx, paneID, keys...)
}

func (c *scopedClient) Reachable(ctx context.Context) error {
	return c.inner.Reachable(ctx)
}

// simulateDryRunClient は daemon パッケージの dryRunClient と同じ考え方の decorator｡
// dryRunClient をそのまま使わない理由は､それが daemon のログファイル
// (*applog.Logger) 前提であるのに対し､simulate は「何を送るはずだったか」を
// そのままコマンドの標準出力 w に表示したいから｡
type simulateDryRunClient struct {
	inner multiplexer.Client
	w     io.Writer
}

func (c simulateDryRunClient) PaneList(ctx context.Context) ([]multiplexer.Pane, error) {
	return c.inner.PaneList(ctx)
}

func (c simulateDryRunClient) PaneGet(ctx context.Context, paneID string) (multiplexer.Pane, error) {
	return c.inner.PaneGet(ctx, paneID)
}

func (c simulateDryRunClient) PaneRead(ctx context.Context, paneID string, opts multiplexer.PaneReadOptions) (string, error) {
	return c.inner.PaneRead(ctx, paneID, opts)
}

func (c simulateDryRunClient) Reachable(ctx context.Context) error {
	return c.inner.Reachable(ctx)
}

func (c simulateDryRunClient) SendText(_ context.Context, paneID, text string) error {
	_, _ = fmt.Fprintf(c.w, "  [dry-run] pane %s へ送るはずだったテキスト: %q\n", paneID, text)
	return nil
}

func (c simulateDryRunClient) SendKeys(_ context.Context, paneID string, keys ...string) error {
	_, _ = fmt.Fprintf(c.w, "  [dry-run] pane %s へ送るはずだったキー: %s\n", paneID, strings.Join(keys, " "))
	return nil
}
