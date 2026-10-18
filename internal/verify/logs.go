package verify

import (
	"fmt"
	"io"

	"github.com/usadamasa/agents-daemon/internal/applog"
	"github.com/usadamasa/agents-daemon/internal/apppath"
)

// DefaultLogTailLines は `logs` が既定で表示する行数。
const DefaultLogTailLines = 80

// RunLogs は現行ログの末尾 n 行を w へ書く。ログが無いこと自体は異常では
// ないので (daemon 未起動、あるいは報告すべき出来事が何も起きていない)、
// エラーにせずその旨を伝える。
func RunLogs(w io.Writer, p apppath.Paths, n int) error {
	if n <= 0 {
		return fmt.Errorf("-n には 1 以上を指定してください (指定値: %d)", n)
	}
	tail, err := applog.TailLogLines(p.LogFile(), n)
	if err != nil {
		return err
	}
	if len(tail) == 0 {
		_, _ = fmt.Fprintln(w, "ログはまだありません")
		return nil
	}
	for _, line := range tail {
		_, _ = fmt.Fprintln(w, line)
	}
	return nil
}
