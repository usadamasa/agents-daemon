package verify

import (
	"fmt"
	"io"

	"github.com/usadamasa/agents-daemon/internal/applog"
)

// DefaultLogTailLines は `logs` が既定で表示する行数。
const DefaultLogTailLines = 80

// LogsReport は `logs` が読んだログのパスと末尾の行。`--json` はこれをそのまま出す。
type LogsReport struct {
	Path  string   `json:"path"`
	Lines []string `json:"lines"`
}

// RunLogs は現行ログ path (daemon.log か、出来事の events.log) の末尾 n 行を w へ書く。
// ログが無いこと自体は異常ではないので (daemon 未起動、あるいは報告すべき出来事が何も起きていない)、
// エラーにせずその旨を伝える。
func RunLogs(w io.Writer, path string, n int) error {
	rep, err := CollectLogs(path, n)
	if err != nil {
		return err
	}
	if len(rep.Lines) == 0 {
		_, _ = fmt.Fprintln(w, "ログはまだありません")
		return nil
	}
	for _, line := range rep.Lines {
		_, _ = fmt.Fprintln(w, line)
	}
	return nil
}

// CollectLogs は path の末尾 n 行を集める。ログが無ければ Lines は空の配列。
func CollectLogs(path string, n int) (LogsReport, error) {
	if n <= 0 {
		return LogsReport{}, fmt.Errorf("-n には 1 以上を指定してください (指定値: %d)", n)
	}
	tail, err := applog.TailLogLines(path, n)
	if err != nil {
		return LogsReport{}, err
	}
	if tail == nil {
		tail = []string{}
	}
	return LogsReport{Path: path, Lines: tail}, nil
}
