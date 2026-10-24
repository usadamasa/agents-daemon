package statusline

import (
	"errors"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/usadamasa/agents-daemon/internal/apppath"
	"github.com/usadamasa/agents-daemon/internal/sessionstate"
	"github.com/usadamasa/agents-daemon/internal/spend"
)

// SpendCapEnv は月額上限 (USD) を渡す環境変数。設定したときだけ cost の合算を記録し、表示する。
// rate_limits が届かない契約 (Enterprise の seat) 向けで、届くプロファイルでは設定しない。
// 上限は人ごとに決まる固定値なので settings の env で渡す (claude-config の work profile)。
const SpendCapEnv = "CLAUDE_STATUSLINE_SPEND_CAP"

// Run は `agents-daemon statusline` の本体。stdin を読み切り、state file を書いてから描画を組む。
// 描画が組めても state の書き込みが失敗していれば、描画と一緒にその理由を返す。
// 環境 (COLUMNS、HOME、SpendCapEnv) は getenv から読む。
func Run(stdin io.Reader, p apppath.Paths, getenv func(string) string, now time.Time) (string, error) {
	data, err := io.ReadAll(stdin)
	if err != nil {
		return "", fmt.Errorf("stdin の読み込みに失敗: %w", err)
	}
	store := sessionstate.New(p.StateDir())
	ledger := spend.New(p.StateDir())

	// 表示と無関係な副作用は幅の判定より前に行う。hidden でも daemon の state は更新する。
	var errs []error
	if err := store.IngestStatusline(data, now); err != nil {
		errs = append(errs, err)
	}
	in, err := parseInput(data)
	if err != nil {
		return "", errors.Join(append(errs, err)...)
	}
	spendCap := parseSpendCap(getenv(SpendCapEnv))
	if spendCap > 0 {
		if err := ledger.Record(in.SessionID, in.Cost, now); err != nil {
			errs = append(errs, err)
		}
	}

	layout := modeFor(getenv("COLUMNS"))
	if layout == ModeHidden {
		return "", errors.Join(errs...)
	}
	v, err := buildView(ledger, in, layout, spendCap, getenv("HOME"), now)
	if err != nil {
		errs = append(errs, err)
	}
	return v.render(now), errors.Join(errs...)
}

// buildView は settings・git・state から描画に要る値を引く。合算は full でだけ読む
// (compact では出さないので、台帳の走査を省く)。
// state が読めなくても描画は返し、理由をエラーに添える。
func buildView(ledger spend.Ledger, in input, layout mode, spendCap int, home string, now time.Time) (view, error) {
	v := view{
		input:  in,
		Mode:   layout,
		Home:   home,
		Effort: readEffort(home),
		Branch: gitBranch(in.CurrentDir),
	}
	if outsideProject(in.CurrentDir, in.ProjectDir) {
		v.ProjectBranch = projectBranch(in.ProjectDir)
	}
	if layout != ModeFull {
		return v, nil
	}

	if spendCap > 0 {
		sum, err := ledger.Sum(now)
		if err != nil {
			return v, err
		}
		v.Spend = &spendView{Day: sum.Day, Month: sum.Month, Cap: spendCap}
	}
	return v, nil
}

// parseSpendCap は環境変数の月額上限を読む。整数でなければ未設定 (0) として扱う。
func parseSpendCap(value string) int {
	capUSD, err := strconv.Atoi(value)
	if err != nil || capUSD <= 0 {
		return 0
	}
	return capUSD
}
