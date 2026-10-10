// Package hookreport は Claude Code の hook から呼ばれ､セッションの状態を端末多重化ツールへ写す処理を持つ｡
// hook script は stdin をバイナリへ渡すだけで､判定と呼び出しの組み立てはここに閉じる｡
package hookreport

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/usadamasa/agents-daemon/internal/herdrcli"
)

// TitleSource は workspace metadata の報告元｡agents-config の herdr-claude-title.sh が使っていた値をそのまま使う｡
// 移行期間は両方の hook が同じ source で報告し､seq の新しい方が勝つ｡別の source にすると古い方の token が
// herdr に残る (TTL を付けていないため)｡
const TitleSource = "claude-config:claude-title"

// herdr の space 欄に出す custom token の名前｡描く側は利用者の herdr config (`$claude` と `$branch` の行)｡
const (
	titleToken  = "claude"
	branchToken = "branch"
)

// HerdrClient は Title が使う herdr の socket API の操作｡実体は herdrcli.SocketClient｡
type HerdrClient interface {
	PaneCurrent(ctx context.Context, callerPaneID string) (herdrcli.PaneView, error)
	ReportWorkspaceMetadata(ctx context.Context, workspaceID, source string, seq int64, tokens map[string]string) error
	TabGet(ctx context.Context, tabID string) (herdrcli.TabView, error)
	TabRename(ctx context.Context, tabID, label string) error
}

// TitleDeps は Title の外部依存｡テストが差し替える｡
type TitleDeps struct {
	Getenv    func(string) string
	NewClient func(socketPath string) HerdrClient
	// Branch は dir の checkout 中のブランチ名を返す｡git の外や dir が無いときは空｡
	Branch func(ctx context.Context, dir string) string
	Now    func() time.Time
}

// DefaultTitleDeps は本番の依存｡
func DefaultTitleDeps() TitleDeps {
	return TitleDeps{
		Getenv:    os.Getenv,
		NewClient: func(socketPath string) HerdrClient { return herdrcli.NewSocketClient(socketPath) },
		Branch:    GitBranch,
		Now:       time.Now,
	}
}

// Title は Claude Code のセッション名 (端末タイトル) と今いるブランチを herdr の space 欄 (workspace metadata の
// token) とタブ行 (タブの label) へ写す｡herdr の pane の外 (HERDR_ENV=1 と HERDR_SOCKET_PATH と HERDR_PANE_ID が
// そろっていない) なら何もせず false を返す｡herdr の中で rex を動かしていても (REX_BLOCK があっても) 外側の
// herdr へ報告する｡
//
// 書き込み先のタブと workspace は `pane.current` から取る｡herdr が注入する HERDR_TAB_ID / HERDR_WORKSPACE_ID は
// pane 作成時の値のままで､`herdr pane move` で移した後は元のタブを指し続けるため｡HERDR_PANE_ID は古くても
// herdr が今の pane へ解決する｡
//
// space 欄とタブ行は独立した面なので､片方が失敗してももう片方は更新し､エラーはまとめて返す｡
func Title(ctx context.Context, deps TitleDeps) (bool, error) {
	socketPath, paneID := deps.Getenv("HERDR_SOCKET_PATH"), deps.Getenv("HERDR_PANE_ID")
	if deps.Getenv("HERDR_ENV") != "1" || socketPath == "" || paneID == "" {
		return false, nil
	}
	client := deps.NewClient(socketPath)

	pane, err := client.PaneCurrent(ctx, paneID)
	if err != nil {
		return false, err
	}
	if pane.TabID == "" || pane.WorkspaceID == "" {
		return false, nil
	}

	// 端末タイトルに混ざる改行やタブを 1 つの空白に畳む｡
	title := strings.Join(strings.Fields(pane.TerminalTitleStripped), " ")
	// セッションの現在地は foreground_cwd (EnterWorktree で動く)｡git の外なら pane を作った cwd で引く｡
	branch := deps.Branch(ctx, pane.ForegroundCWD)
	if branch == "" {
		branch = deps.Branch(ctx, pane.CWD)
	}

	var errs []error
	tokens := map[string]string{titleToken: title, branchToken: branch}
	if err := client.ReportWorkspaceMetadata(ctx, pane.WorkspaceID, TitleSource, deps.Now().UnixNano(), tokens); err != nil {
		errs = append(errs, err)
	}
	if err := renameTab(ctx, client, pane.TabID, title); err != nil {
		errs = append(errs, err)
	}
	return true, errors.Join(errs...)
}

// renameTab はタブの label を title にする｡title が空なら連番へ戻す (`tab.rename` に空を渡すと空の label になり､
// タブ行から手掛かりが消える)｡label が既に同じなら呼ばない｡
func renameTab(ctx context.Context, client HerdrClient, tabID, title string) error {
	tab, err := client.TabGet(ctx, tabID)
	if err != nil {
		return err
	}
	label := title
	if label == "" && tab.Number > 0 {
		label = strconv.Itoa(tab.Number)
	}
	if label == "" || label == tab.Label {
		return nil
	}
	return client.TabRename(ctx, tabID, label)
}

// GitBranch は dir で checkout 中のブランチ名を返す｡dir が空か存在しない､git の外､detached HEAD､git が無い､
// ctx の期限切れのときは空｡hook から呼ばれるので失敗を返さず「出せない」に倒す｡
func GitBranch(ctx context.Context, dir string) string {
	if dir == "" {
		return ""
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return ""
	}
	// #nosec G204 -- 引数は固定で､dir は herdr が返した pane の cwd｡
	cmd := exec.CommandContext(ctx, "git", "-C", dir, "branch", "--show-current")
	cmd.Stderr = nil
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return string(bytes.TrimSpace(out))
}
