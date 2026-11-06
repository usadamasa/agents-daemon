package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// Daemon は agents-daemon のバイナリを呼ぶ Service。フロントエンドが Call.ByName("main.Daemon.<method>") で呼ぶ。
// protocol の Go の型は import せず、stdout の JSON をそのままフロントエンドへ返す (型は types/index.d.ts)。
// 状態の読み書きはバイナリが持ち、ここは exec して結果を返すだけにする。
type Daemon struct {
	bin string
}

func newDaemon() *Daemon {
	return &Daemon{bin: daemonBin()}
}

// daemonBin はバイナリの置き場。hooks/lib/daemon-bin.sh、hooks/daemon-bin.ts、Taskfile.yml の install と同じ規則で、
// 変えるときは 4 つを揃える (AGENTS.md)。
func daemonBin() string {
	cache := os.Getenv("XDG_CACHE_HOME")
	if cache == "" {
		cache = filepath.Join(os.Getenv("HOME"), ".cache")
	}
	return filepath.Join(cache, "agents-daemon", "bin", "agents-daemon")
}

// runTimeout は mod の runDaemon と同じ 10 秒。spawnTimeout は claude が前景になるまでバイナリが待つ分 (mod と同じ 30 秒)。
// tellTimeout は rex を数回叩いて文面と Enter の間に待つ分 (rex 1 回は 10 秒で切れる)。
const (
	runTimeout   = 10 * time.Second
	spawnTimeout = 30 * time.Second
	tellTimeout  = 20 * time.Second
)

// commands はフロントエンドが Run で呼べるサブコマンドと、その timeout。stdin の形は protocol.schema.json の
// <Command>Request。ここに無いコマンドは呼べない (フロントエンドの文字列をそのまま exec に渡さない)。
// 出来事のログはどのコマンドもバイナリが書くので、event-log は無い。
var commands = map[string]time.Duration{
	"park set":     runTimeout,
	"park discard": runTimeout,
	"park adopt":   runTimeout,
	"park hand":    spawnTimeout,
	"edge add":     runTimeout,
	"edge rm":      runTimeout,
	"task dismiss": runTimeout,
	"spawn":        spawnTimeout,
	"tell":         tellTimeout,
}

// Deps は `agents-daemon deps` の stdout (DepsData の JSON) を返す。
func (d *Daemon) Deps() (string, error) {
	return d.run(runTimeout, "{}", "deps")
}

// Run は command (commands の鍵) を stdin の JSON で走らせ、stdout を返す。
func (d *Daemon) Run(command, stdin string) (string, error) {
	timeout, ok := commands[command]
	if !ok {
		return "", fmt.Errorf("agent-tasks から呼べないコマンド: %q", command)
	}
	return d.run(timeout, stdin, strings.Fields(command)...)
}

// PickFolder は macOS のフォルダ選択の dialog を開き、選んだ絶対パスを返す (選ばなければ空)。
func (d *Daemon) PickFolder() (string, error) {
	return application.Get().Dialog.OpenFile().
		CanChooseDirectories(true).
		CanChooseFiles(false).
		CanCreateDirectories(true).
		SetTitle("新しいセッションを起こす repo").
		PromptForSingleSelection()
}

// run は args のサブコマンドを走らせ、stdin を渡し、stdout を返す。非 0 で終わったら stderr を error にする。
func (d *Daemon) run(timeout time.Duration, stdin string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, d.bin, args...) // #nosec G204 -- shell を通さず、bin は固定の置き場、args はコードの定数
	cmd.Stdin = strings.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if exit, ok := errors.AsType[*exec.ExitError](err); ok {
			msg := strings.TrimSpace(stderr.String())
			if msg == "" {
				msg = fmt.Sprintf("exit %d", exit.ExitCode())
			}
			return "", fmt.Errorf("agents-daemon %s: %s", strings.Join(args, " "), msg)
		}
		return "", fmt.Errorf("%s: %w", d.bin, err)
	}
	return stdout.String(), nil
}
