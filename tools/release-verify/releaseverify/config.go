package releaseverify

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/usadamasa/agents-daemon/internal/apppath"
	"github.com/usadamasa/agents-daemon/tools/release-verify/releaseverify/command"
)

// daemonRun は daemon を回す時間。tick の既定は 5 秒なので、数 tick 回る長さにする。
const daemonRun = 20 * time.Second

// FromEnv は、cwd の repo を検証の対象に、常用の環境を複製元にした Config を作る。
// 置き場と複製元を環境変数で差し替える入口は作らない (書き込みの拒否を外せる入口が本番に残る)。
func FromEnv(ctx context.Context) (Config, error) {
	repo, err := command.GitOutput(ctx, ".", "rev-parse", "--show-toplevel")
	if err != nil {
		return Config{}, refuse("git の repo の中で打つ: %v", err)
	}
	// dream の project は cwd ではなく、worktree の親の checkout から決める (memory はそこに紐づく)。
	commonDir, err := command.GitOutput(ctx, repo, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return Config{}, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return Config{}, err
	}
	paths, err := apppath.FromEnv()
	if err != nil {
		return Config{}, err
	}
	claude, err := apppath.ClaudeConfigDir()
	if err != nil {
		return Config{}, err
	}
	// 常用のバイナリとアプリの置き場は、規則を持つ hooks/lib/daemon-bin.sh に聞く。
	const ask = `SCRIPT_DIR="$1"; . "$1/lib/daemon-bin.sh"; printf '%s\n%s\n' "$DAEMON_BIN" "$GUI_APP"`
	out, err := command.Host(ctx, "bash", "-c", ask, "_", filepath.Join(repo, "hooks")).Output()
	if err != nil {
		return Config{}, fmt.Errorf("常用のバイナリの置き場を解決できない: %w", err)
	}
	bin, app, ok := strings.Cut(strings.TrimSpace(string(out)), "\n")
	if !ok {
		return Config{}, fmt.Errorf("常用のバイナリの置き場を解決できない: %q", out)
	}
	darwin := runtime.GOOS == "darwin"
	return Config{
		Repo: repo,
		// 検証の環境は作り直せるので、バイナリと同じ cache の下に置く。
		Base:         filepath.Join(filepath.Dir(filepath.Dir(bin)), "release-verify"),
		SourceState:  paths.StateDir(),
		SourceClaude: claude,
		SourceConfig: paths.ConfigFile(),
		BaselineBin:  bin,
		RealApp:      app,
		RexSocket:    filepath.Join(home, "Library", "Application Support", "rex", "server.sock"),
		Project:      filepath.Dir(commonDir),
		DaemonRun:    daemonRun,
		BuildApp:     darwin,
		WriteGuard:   darwin,
		Keep:         os.Getenv("KEEP") == "1",
		Stdout:       os.Stdout,
		Stderr:       os.Stderr,
	}, nil
}
