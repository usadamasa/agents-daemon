package worktree

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"
)

// RunHook は SessionStart hook として回収を回す。stdin の hook 入力 (JSON) の cwd を起点にし、
// 回収したときだけ {"systemMessage": ...} を stdout に出す。
// セッション開始を止めないことを最優先にし、git リポジトリ外でも、git が何を返しても nil を返す。
// 失敗は時刻付きで stderr に残す。
func RunHook(stdin io.Reader, stdout, stderr io.Writer, opts Opts) error {
	opts.Cwd = hookCwd(stdin)
	var report bytes.Buffer
	opts.Stdout = &report
	if _, err := Run(opts); err != nil && !errors.Is(err, ErrNotRepository) {
		_, _ = fmt.Fprintf(stderr, "%s prune-worktrees: %v\n", time.Now().Format(time.RFC3339), err)
	}
	if report.Len() == 0 {
		return nil
	}
	out, err := json.Marshal(map[string]string{"systemMessage": report.String()})
	if err != nil {
		return nil
	}
	_, _ = fmt.Fprintln(stdout, string(out))
	return nil
}

// hookCwd は hook 入力の cwd を返す。読めなければプロセスの cwd に落とす。
func hookCwd(stdin io.Reader) string {
	var input struct {
		Cwd string `json:"cwd"`
	}
	if err := json.NewDecoder(stdin).Decode(&input); err == nil && input.Cwd != "" {
		if info, err := os.Stat(input.Cwd); err == nil && info.IsDir() {
			return input.Cwd
		}
	}
	cwd, _ := os.Getwd()
	return cwd
}
