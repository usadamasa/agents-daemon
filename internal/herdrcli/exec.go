package herdrcli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"time"
)

// defaultTimeout は herdr コマンド 1 回あたりのデフォルトタイムアウト｡
// hung した herdr が daemon を巻き込んで停止させないための安全弁｡
const defaultTimeout = 10 * time.Second

// execClient は herdr CLI を subprocess として呼び出す Client 実装｡
type execClient struct {
	binPath string
	timeout time.Duration
}

// NewExecClient は実 herdr バイナリを叩く Client を返す｡
// バイナリパスは環境変数 HERDR_BIN_PATH で上書きできる (未設定なら PATH 上の "herdr")｡
func NewExecClient() Client {
	return newExecClientWithTimeout(defaultTimeout)
}

// newExecClientWithTimeout はコマンド 1 回あたりのタイムアウトを指定して Client を作る｡
// パッケージ外からタイムアウトを指定する必要はまだ無いため (実機統合テストのみが使う)､
// 非公開のままにしてある｡
func newExecClientWithTimeout(timeout time.Duration) Client {
	bin := os.Getenv("HERDR_BIN_PATH")
	if bin == "" {
		bin = "herdr"
	}
	return &execClient{binPath: bin, timeout: timeout}
}

// run は herdr を実行し､stdout/stderr を分離したまま返す｡
// ctx には呼び出し単位のタイムアウトを必ず被せる (exec.CommandContext)｡
// timeout が 0 以下なら制限時間を課さない｡テストがマシンの速度に依存しないよう､
// 「タイムアウトを検証しないテスト」は時間の制約そのものを外して回すためにある｡
func (c *execClient) run(ctx context.Context, args ...string) (stdout, stderr []byte, err error) {
	runCtx := ctx
	if c.timeout > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, c.timeout)
		defer cancel()
	}

	// #nosec G204 -- binPath は "herdr" か HERDR_BIN_PATH の値 (どちらも利用者自身の
	// 環境)､args はこのパッケージ内で組み立てた固定のサブコマンドと pane ID のみ｡
	cmd := exec.CommandContext(runCtx, c.binPath, args...)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	// Stdout/Stderr に io.Writer を渡すと os/exec がパイプを作り､Wait はその
	// コピーが終わるまで戻らない｡ctx の期限で herdr 本体を kill しても､
	// パイプを継承した孫プロセスが生きているとそこで待たされ､タイムアウトが
	// 実質効かなくなる (Linux の CI で 50ms の期限に対し 2 秒かかった)｡
	// WaitDelay を入れると､期限超過後はこの猶予でパイプを強制的に閉じる｡
	cmd.WaitDelay = 100 * time.Millisecond
	runErr := cmd.Run()

	if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		return outBuf.Bytes(), errBuf.Bytes(), fmt.Errorf("herdr %v: %s でタイムアウトした: %w", args, c.timeout, runCtx.Err())
	}
	return outBuf.Bytes(), errBuf.Bytes(), runErr
}

// runEnvelope は herdr を実行し JSON envelope を取り出す｡
// herdr は pane_not_found のような既知のエラーでも exit code 1 と共に
// 解釈可能な envelope を返すため､非 0 exit だけでは失敗と決めつけず､
// envelope が取れればその中身 (env.Error) を優先する｡
func (c *execClient) runEnvelope(ctx context.Context, args ...string) (*envelope, error) {
	stdout, stderr, runErr := c.run(ctx, args...)
	env, parseErr := parseEnvelope(stdout, stderr)
	if parseErr != nil {
		if runErr != nil {
			return nil, fmt.Errorf("herdr %v: %w (stderr=%q)", args, runErr, stderr)
		}
		return nil, parseErr
	}
	if env.Error != nil {
		return nil, env.Error
	}
	return env, nil
}

// runVoid は結果の JSON を必要としないコマンド (pane を書き換える系) を実行する｡
//
// herdr 0.8.0 の send-text / send-keys / report-metadata は､成功したとき
// stdout にも stderr にも何も出さず､終了コード 0 だけを返す (実測)｡
// これらに envelope を要求すると､成功をそのまま失敗と取り違える｡
// 実際､送信は届いているのに「envelope を取り出せなかった」で再試行上限まで
// 突き進み､自動再開が丸ごと機能しない状態になった｡
//
// そこで終了コードを一次情報とし､出力があってそれがエラー envelope だった
// ときだけ､その内容をエラーとして返す｡
func (c *execClient) runVoid(ctx context.Context, args ...string) error {
	stdout, stderr, runErr := c.run(ctx, args...)
	if env, parseErr := parseEnvelope(stdout, stderr); parseErr == nil && env.Error != nil {
		return env.Error
	}
	if runErr != nil {
		return fmt.Errorf("herdr %v: %w (stderr=%q)", args, runErr, stderr)
	}
	return nil
}

type paneListResult struct {
	Panes []Pane `json:"panes"`
}

func (c *execClient) PaneList(ctx context.Context) ([]Pane, error) {
	env, err := c.runEnvelope(ctx, "pane", "list")
	if err != nil {
		return nil, err
	}
	var res paneListResult
	if err := json.Unmarshal(env.Result, &res); err != nil {
		return nil, fmt.Errorf("herdr pane list: result の parse に失敗: %w", err)
	}
	return res.Panes, nil
}

type paneGetResult struct {
	Pane Pane `json:"pane"`
}

func (c *execClient) PaneGet(ctx context.Context, paneID string) (Pane, error) {
	env, err := c.runEnvelope(ctx, "pane", "get", paneID)
	if err != nil {
		return Pane{}, err
	}
	var res paneGetResult
	if err := json.Unmarshal(env.Result, &res); err != nil {
		return Pane{}, fmt.Errorf("herdr pane get %s: result の parse に失敗: %w", paneID, err)
	}
	return res.Pane, nil
}

// PaneRead は herdr pane read の出力をそのまま返す｡この応答は envelope に
// 包まれない生テキストであり､JSON parse を試みない｡
func (c *execClient) PaneRead(ctx context.Context, paneID string, opts PaneReadOptions) (string, error) {
	args := []string{"pane", "read", paneID}
	if opts.Source != "" {
		args = append(args, "--source", string(opts.Source))
	}
	if opts.Lines > 0 {
		args = append(args, "--lines", strconv.Itoa(opts.Lines))
	}
	stdout, stderr, runErr := c.run(ctx, args...)
	if runErr != nil {
		return "", fmt.Errorf("herdr %v: %w (stderr=%q)", args, runErr, stderr)
	}
	return string(stdout), nil
}

func (c *execClient) SendText(ctx context.Context, paneID, text string) error {
	return c.runVoid(ctx, "pane", "send-text", paneID, text)
}

func (c *execClient) SendKeys(ctx context.Context, paneID string, keys ...string) error {
	args := append([]string{"pane", "send-keys", paneID}, keys...)
	return c.runVoid(ctx, args...)
}

func (c *execClient) ReportMetadata(ctx context.Context, paneID string, opts ReportMetadataOptions) error {
	if opts.Source == "" {
		return fmt.Errorf("herdr pane report-metadata %s: Source は必須", paneID)
	}

	args := []string{"pane", "report-metadata", paneID, "--source", opts.Source}
	if opts.Agent != "" {
		args = append(args, "--agent", opts.Agent)
	}
	if opts.ClearStateLabels {
		args = append(args, "--clear-state-labels")
	}
	// map の走査順は非決定的なので､コマンドライン引数を再現可能にするためキーでソートする｡
	if len(opts.StateLabels) > 0 {
		statuses := make([]string, 0, len(opts.StateLabels))
		for status := range opts.StateLabels {
			statuses = append(statuses, string(status))
		}
		sort.Strings(statuses)
		for _, status := range statuses {
			args = append(args, "--state-label", fmt.Sprintf("%s=%s", status, opts.StateLabels[AgentStatus(status)]))
		}
	}
	if opts.TTLMillis > 0 {
		args = append(args, "--ttl-ms", strconv.Itoa(opts.TTLMillis))
	}

	return c.runVoid(ctx, args...)
}

// statusResult は `herdr status --json` の応答形｡
// これは pane 系サブコマンドと違い envelope ({"id":...,"result":...}) に
// 包まれないトップレベルの JSON なので､envelope.go の parseEnvelope とは別に扱う｡
type statusResult struct {
	Server struct {
		Running bool `json:"running"`
	} `json:"server"`
}

// Reachable は `herdr status --json` を叩いて herdr server の生死を確認する｡
//
// HERDR_SOCKET_PATH は herdr が起動した pane の中でのみ設定される環境変数であり､
// daemon は SessionStart hook から起動されて detach した後も長く動き続けるため､
// 起動時に継承した値が herdr 再起動後も有効である保証がない｡一方
// `herdr status --json` はソケットパスを自己解決し､HERDR_SOCKET_PATH や
// HERDR_ENV が未設定でも (実機で確認済み) 正しく server 状態を返す｡そのため
// 環境変数を信頼するのではなく､この実プローブを到達性確認の手段として使う｡
func (c *execClient) Reachable(ctx context.Context) error {
	stdout, stderr, runErr := c.run(ctx, "status", "--json")
	if runErr != nil {
		return fmt.Errorf("herdr status --json: %w (stderr=%q)", runErr, stderr)
	}
	var st statusResult
	if err := json.Unmarshal(bytes.TrimSpace(stdout), &st); err != nil {
		return fmt.Errorf("herdr status --json: 応答の parse に失敗: %w (stdout=%q)", err, stdout)
	}
	if !st.Server.Running {
		return fmt.Errorf("herdr server が起動していない")
	}
	return nil
}
