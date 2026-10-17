package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/usadamasa/agents-daemon/internal/apppath"
	"github.com/usadamasa/agents-daemon/internal/sessionstate"
	"github.com/usadamasa/agents-daemon/internal/spend"
	"github.com/usadamasa/agents-daemon/internal/statusline"
)

// spendCapEnv は月額上限 (USD) を渡す環境変数。設定したときだけ cost の合算を記録し、表示する。
// rate_limits が届かない契約 (Enterprise の seat) 向けで、届くプロファイルでは設定しない。
// 上限は人ごとに決まる固定値なので settings の env で渡す (claude-config の work profile)。
const spendCapEnv = "CLAUDE_STATUSLINE_SPEND_CAP"

// newStatuslineCmd は `agents-daemon statusline` を組み立てる。
//
// settings の statusLine.command から描画のたびに呼ばれ、stdin の JSON を受けて描画を stdout に出す。
// 同じプロセスで ingest-statusline と同じ state file (context/ と rate-limits/) と cost の記録も
// 書くので、描画ごとの fork は 1 つで済む。失敗しても nil を返して exit 0 にするのは statusline が
// fail-silent でなければならないため (非 0 は status バーを汚す)。理由は stderr に出す。
func newStatuslineCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "statusline",
		Short: "Claude Code の statusline を描画し、同じプロセスで daemon が読む state file を書く",
		Long: "Claude Code が statusline コマンドへ渡す JSON を stdin で受け取り、3 行 (幅が足りなければ 1 行、\n" +
			"さらに狭ければ何も出さない) の statusline を stdout に出す。描画の前に ingest-statusline と同じ\n" +
			"state file を書き、" + spendCapEnv + " があれば cost の合算も記録する。\n" +
			"settings.json には次のように書く:\n\n" +
			`  "statusLine": {"type": "command", "command": "${XDG_CACHE_HOME:-$HOME/.cache}/agents-daemon/bin/agents-daemon statusline", "padding": 0}` + "\n\n" +
			"失敗しても exit 0 で終わる (理由は stderr)。",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			out, err := runStatusline(cmd.InOrStdin(), time.Now())
			if err != nil {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "statusline:", err)
			}
			_, _ = fmt.Fprint(cmd.OutOrStdout(), out)
			return nil
		},
	}
}

// runStatusline は stdin を読み切り、state file を書いてから描画を組む。
// 描画が組めても state の書き込みが失敗していれば、描画と一緒にその理由を返す。
func runStatusline(stdin io.Reader, now time.Time) (string, error) {
	input, err := io.ReadAll(stdin)
	if err != nil {
		return "", fmt.Errorf("stdin の読み込みに失敗: %w", err)
	}
	p, err := apppath.FromEnv()
	if err != nil {
		return "", err
	}
	store := sessionstate.New(p.StateDir())
	ledger := spend.New(p.StateDir())

	// 表示と無関係な副作用は幅の判定より前に行う。hidden でも daemon の state は更新する。
	var errs []error
	if err := store.IngestStatusline(input, now); err != nil {
		errs = append(errs, err)
	}
	in, err := statusline.ParseInput(input)
	if err != nil {
		return "", errors.Join(append(errs, err)...)
	}
	spendCap := parseSpendCap(os.Getenv(spendCapEnv))
	if spendCap > 0 {
		if err := ledger.Record(in.SessionID, in.Cost, now); err != nil {
			errs = append(errs, err)
		}
	}

	mode := statusline.ModeFor(os.Getenv("COLUMNS"))
	if mode == statusline.ModeHidden {
		return "", errors.Join(errs...)
	}
	view, err := buildView(ledger, in, mode, spendCap, now)
	if err != nil {
		errs = append(errs, err)
	}
	return view.Render(now), errors.Join(errs...)
}

// buildView は settings・git・state から描画に要る値を引く。合算は full でだけ読む
// (compact では出さないので、台帳の走査を省く)。
// state が読めなくても描画は返し、理由をエラーに添える。
func buildView(ledger spend.Ledger, in statusline.Input, mode statusline.Mode, spendCap int, now time.Time) (statusline.View, error) {
	home := os.Getenv("HOME")
	view := statusline.View{
		Input:  in,
		Mode:   mode,
		Home:   home,
		Effort: statusline.ReadEffort(home),
		Branch: statusline.GitBranch(in.CurrentDir),
	}
	if statusline.OutsideProject(in.CurrentDir, in.ProjectDir) {
		view.ProjectBranch = statusline.ProjectBranch(in.ProjectDir)
	}
	if mode != statusline.ModeFull {
		return view, nil
	}

	if spendCap > 0 {
		sum, err := ledger.Sum(now)
		if err != nil {
			return view, err
		}
		view.Spend = &statusline.SpendView{Day: sum.Day, Month: sum.Month, Cap: spendCap}
	}
	return view, nil
}

// parseSpendCap は環境変数の月額上限を読む。整数でなければ未設定 (0) として扱う。
func parseSpendCap(value string) int {
	capUSD, err := strconv.Atoi(value)
	if err != nil || capUSD <= 0 {
		return 0
	}
	return capUSD
}
