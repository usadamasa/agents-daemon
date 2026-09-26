package verify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/usadamasa/agents-daemon/internal/apppath"
	"github.com/usadamasa/agents-daemon/internal/config"
	"github.com/usadamasa/agents-daemon/internal/detect"
	"github.com/usadamasa/agents-daemon/internal/herdrcli"
	"github.com/usadamasa/agents-daemon/internal/monitor"
	"github.com/usadamasa/agents-daemon/internal/sessionstate"
)

// simulateMetadataSource は simulate が herdr へ渡す --source 識別子｡
// daemon パッケージ本体の metadataSource ("agents-daemon") とは別の値にして､
// 本番の daemon が書いたラベルと simulate の後始末を取り違えないようにする｡
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
func RunSimulate(ctx context.Context, w io.Writer, p apppath.Paths, opts SimulateOptions) error {
	if os.Getenv("HERDR_ENV") != "1" {
		return errors.New("HERDR_ENV=1 の herdr セッション内で実行してください (使い捨て pane を割り出す基準になる自分の pane が要ります)")
	}

	execClient := herdrcli.NewExecClient()
	if err := execClient.Reachable(ctx); err != nil {
		return fmt.Errorf("herdr に到達できません: %w", err)
	}

	_, _ = fmt.Fprintln(w, "使い捨て pane を作成します...")
	ownedPaneID, err := splitSimulatePane(ctx)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(w, "  pane %s を作成しました\n", ownedPaneID)
	defer cleanupSimulatePane(ctx, w, ownedPaneID, opts.Keep)

	if err := markAsClaudeAgent(ctx, ownedPaneID); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(w, "  pane %s を Claude agent として herdr に登録しました (agent=claude, state=idle)\n", ownedPaneID)

	// 作りたての pane は shell 起動処理 (direnv の読み込み等) の途中のことがあり､
	// その間に送った `herdr pane run` のコマンドが消化されず失われることがある
	// (実測)｡起動が落ち着くまで少し待ってから画面テキストを書き込む｡
	time.Sleep(shellStartupDelay)

	screenContent := defaultSimulateScreen
	if opts.ScreenFile != "" {
		data, readErr := os.ReadFile(opts.ScreenFile) // #nosec G304 -- CLI 引数で明示指定
		if readErr != nil {
			return fmt.Errorf("--screen-file の読み込みに失敗: %w", readErr)
		}
		screenContent = string(data)
	}
	if err := writeScreenIntoPane(ctx, execClient, ownedPaneID, screenContent); err != nil {
		return err
	}
	_, _ = fmt.Fprintln(w, "  limit 風の画面を書き込みました")

	return runSimulateTicks(ctx, w, p, ownedPaneID, execClient, opts)
}

// splitSimulatePane は自分専用の使い捨て pane を作り､その pane_id を返す｡
// 現在の pane (--current) から分割するだけなので､現在の pane の内容には影響しない｡
func splitSimulatePane(ctx context.Context) (string, error) {
	out, err := runHerdrRaw(ctx, "pane", "split", "--current", "--direction", "down", "--ratio", "0.2", "--no-focus")
	if err != nil {
		return "", fmt.Errorf("使い捨て pane の作成に失敗: %w", err)
	}
	var env struct {
		Result struct {
			Pane struct {
				PaneID string `json:"pane_id"`
			} `json:"pane"`
		} `json:"result"`
		Error *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(out), &env); err != nil {
		return "", fmt.Errorf("pane split の応答を解釈できません: %w (stdout=%q)", err, out)
	}
	if env.Error != nil {
		return "", fmt.Errorf("herdr pane split: %s (%s)", env.Error.Message, env.Error.Code)
	}
	if env.Result.Pane.PaneID == "" {
		return "", fmt.Errorf("pane split の応答に pane_id が含まれていません (stdout=%q)", out)
	}
	return env.Result.Pane.PaneID, nil
}

// markAsClaudeAgent は herdr pane report-agent で paneID を Claude agent に見せかける｡
// これにより herdrcli.IsClaudeAgent(pane) が true になり､monitor.Tick が daemon と
// 同じ経路でこの pane を対象として扱うようになる｡
func markAsClaudeAgent(ctx context.Context, paneID string) error {
	if _, err := runHerdrRaw(ctx, "pane", "report-agent", paneID, "--source", simulateMetadataSource, "--agent", "claude", "--state", "idle"); err != nil {
		return fmt.Errorf("pane %s を Claude agent として登録できませんでした: %w", paneID, err)
	}
	return nil
}

// writeScreenTimeout は writeScreenIntoPane が画面への反映を待つ最大時間｡
const writeScreenTimeout = 5 * time.Second

// shellStartupDelay は使い捨て pane を作ってから画面テキストを書き込むまで待つ時間｡
const shellStartupDelay = 1500 * time.Millisecond

// writeScreenIntoPane は herdr pane run で paneID のシェルに画面テキストを cat させ､
// 実際に画面へ反映されるまで待つ｡send-text だと文字ごとの入力になり､内容によっては
// シェルへのコマンドとして解釈されかねない (実機確認済み: `herdr pane run <id> cat <file>`
// なら静的な表示に留まる)｡`herdr pane run` はレンダリング完了を待たずに戻ることがある
// (実測: 固定 500ms 待機では画面反映前に読んでしまい none 判定になることがあった) ため､
// 固定 sleep ではなく画面に内容が現れたことを client.PaneRead で確認してから返す｡
func writeScreenIntoPane(ctx context.Context, client herdrcli.Client, paneID, content string) error {
	tmp, err := os.CreateTemp("", "agents-daemon-simulate-*.txt")
	if err != nil {
		return fmt.Errorf("画面テキストの一時ファイル作成に失敗: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()

	if _, err := tmp.WriteString(content); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("画面テキストの書き込みに失敗: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("画面テキストの一時ファイルのクローズに失敗: %w", err)
	}

	if _, err := runHerdrRaw(ctx, "pane", "run", paneID, "cat", tmpPath); err != nil {
		return fmt.Errorf("pane %s への画面テキスト書き込みに失敗: %w", paneID, err)
	}
	// herdr pane run はコマンド文字列を pane に入力するだけで Enter は押さない (実測)｡
	// これを送らないと cat が実行されないまま画面には何も反映されない｡
	time.Sleep(300 * time.Millisecond)
	if _, err := runHerdrRaw(ctx, "pane", "send-keys", paneID, "enter"); err != nil {
		return fmt.Errorf("pane %s への enter 送信に失敗: %w", paneID, err)
	}

	marker := lastNonEmptyLine(content)
	if marker == "" {
		return nil // 空画面なら待つ対象が無い
	}
	return waitForScreenContent(ctx, client, paneID, marker, writeScreenTimeout)
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

// waitForScreenContent は paneID の画面 (detection source) に substr が現れるまで
// ポーリングする｡
func waitForScreenContent(ctx context.Context, client herdrcli.Client, paneID, substr string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastScreen string
	for {
		screen, err := client.PaneRead(ctx, paneID, herdrcli.PaneReadOptions{Source: herdrcli.SourceDetection, Lines: 40})
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
// splitSimulatePane がこのプロセス自身で作った値であり､呼び出し元にそれ以外の
// pane_id を渡す手段は無い｡失敗しても simulate 全体は失敗させず､手動での
// 後始末コマンドを案内するだけに留める｡
func cleanupSimulatePane(ctx context.Context, w io.Writer, ownedPaneID string, keep bool) {
	if keep {
		_, _ = fmt.Fprintf(w, "--keep が指定されたため pane %s は残しています (`herdr pane close %s` で手動削除してください)\n", ownedPaneID, ownedPaneID)
		return
	}
	var errs []error
	if _, err := runHerdrRaw(ctx, "pane", "release-agent", ownedPaneID, "--source", simulateMetadataSource, "--agent", "claude"); err != nil {
		errs = append(errs, err)
	}
	if _, err := runHerdrRaw(ctx, "pane", "close", ownedPaneID); err != nil {
		errs = append(errs, err)
	}
	if err := errors.Join(errs...); err != nil {
		_, _ = fmt.Fprintf(w, "後始末に失敗しました (`herdr pane close %s` で手動削除してください): %v\n", ownedPaneID, err)
		return
	}
	_, _ = fmt.Fprintf(w, "pane %s を閉じました\n", ownedPaneID)
}

// runHerdrRaw は herdr CLI を直接叩く｡simulate が使う pane split / report-agent /
// release-agent / close は herdrcli.Client interface (PaneList/PaneRead/SendText/
// SendKeys/ReportMetadata/Reachable) に含まれない pane ライフサイクル操作なので､
// このファイル専用の最小限のラッパーとして実装している
// (internal/herdrcli は本タスクの変更対象外)｡
func runHerdrRaw(ctx context.Context, args ...string) ([]byte, error) {
	bin := os.Getenv("HERDR_BIN_PATH")
	if bin == "" {
		bin = "herdr"
	}
	runCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(runCtx, bin, args...) // #nosec G204,G702 -- bin は固定既定値/環境変数､args はこのファイル内で組み立てた定数引数のみ
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return outBuf.Bytes(), fmt.Errorf("herdr %v: %w (stderr=%q)", args, err, errBuf.Bytes())
	}
	return outBuf.Bytes(), nil
}

// runSimulateTicks は monitor.Tick を daemon と同じ設定で実行し､結果を w へ表示する｡
// 1 回目の Tick で limit を検知して待機状態に入った場合､2 回目は起床時刻を過ぎたと
// 仮定した合成の now を渡して再送信の経路 (recoverPane 経由の SendText/SendKeys) まで
// 確認する｡実時間では待たない (上限解除まで数時間かかるため)｡
func runSimulateTicks(ctx context.Context, w io.Writer, p apppath.Paths, ownedPaneID string, execClient herdrcli.Client, opts SimulateOptions) error {
	cfg, cfgErr := config.Load(p.ConfigFile())
	if cfgErr != nil {
		_, _ = fmt.Fprintf(w, "設定の読み込みに失敗､デフォルト値で継続します: %v\n", cfgErr)
		cfg = config.Default()
	}
	classifier, err := detect.NewClassifier(cfg.CustomPatterns, cfg.CustomTransientPatterns)
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
	var tickClient herdrcli.Client = guarded
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

// scopedClient は herdrcli.Client を包み､ownedPaneID 以外の pane を対象にした呼び出しを
// 全て拒否する｡simulate は自分が作った使い捨て pane 以外に絶対に触れてはいけないので､
// 「呼び出し側が正しい pane_id を渡す」という規律に頼るのではなく､型システムの外側で
// 機械的に強制できるガードとして実装する｡
type scopedClient struct {
	inner       herdrcli.Client
	ownedPaneID string
}

var errPaneNotOwned = errors.New("simulate が作成した pane 以外への操作を拒否しました")

func newScopedClient(inner herdrcli.Client, ownedPaneID string) *scopedClient {
	return &scopedClient{inner: inner, ownedPaneID: ownedPaneID}
}

func (c *scopedClient) guard(paneID string) error {
	if paneID != c.ownedPaneID {
		return fmt.Errorf("%w (要求された pane=%q, 所有 pane=%q)", errPaneNotOwned, paneID, c.ownedPaneID)
	}
	return nil
}

func (c *scopedClient) PaneList(ctx context.Context) ([]herdrcli.Pane, error) {
	return c.inner.PaneList(ctx) // 読み取り専用の一覧なので許可する
}

func (c *scopedClient) PaneGet(ctx context.Context, paneID string) (herdrcli.Pane, error) {
	if err := c.guard(paneID); err != nil {
		return herdrcli.Pane{}, err
	}
	return c.inner.PaneGet(ctx, paneID)
}

func (c *scopedClient) PaneRead(ctx context.Context, paneID string, opts herdrcli.PaneReadOptions) (string, error) {
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

func (c *scopedClient) ReportMetadata(ctx context.Context, paneID string, opts herdrcli.ReportMetadataOptions) error {
	if err := c.guard(paneID); err != nil {
		return err
	}
	return c.inner.ReportMetadata(ctx, paneID, opts)
}

func (c *scopedClient) Reachable(ctx context.Context) error {
	return c.inner.Reachable(ctx)
}

// simulateDryRunClient は daemon パッケージの dryRunClient と同じ考え方の decorator｡
// dryRunClient をそのまま使わない理由は､それが daemon のログファイル
// (*applog.Logger) 前提であるのに対し､simulate は「何を送るはずだったか」を
// そのままコマンドの標準出力 w に表示したいから｡
type simulateDryRunClient struct {
	inner herdrcli.Client
	w     io.Writer
}

func (c simulateDryRunClient) PaneList(ctx context.Context) ([]herdrcli.Pane, error) {
	return c.inner.PaneList(ctx)
}

func (c simulateDryRunClient) PaneGet(ctx context.Context, paneID string) (herdrcli.Pane, error) {
	return c.inner.PaneGet(ctx, paneID)
}

func (c simulateDryRunClient) PaneRead(ctx context.Context, paneID string, opts herdrcli.PaneReadOptions) (string, error) {
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

func (c simulateDryRunClient) ReportMetadata(_ context.Context, paneID string, _ herdrcli.ReportMetadataOptions) error {
	_, _ = fmt.Fprintf(c.w, "  [dry-run] pane %s のラベル更新はしない\n", paneID)
	return nil
}
