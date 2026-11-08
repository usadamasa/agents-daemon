package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
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
	// open は検査を通った target を既定のアプリで開く。
	open func(target string) error
	// 以下は開いたまま新しいバージョンへ切り替えるためのもの (upgrade.go)。window は main が窓を作った後に入れる。
	upgrader *upgrader
	window   *application.WebviewWindow
	restored *restoreState
}

func newDaemon() *Daemon {
	return &Daemon{bin: daemonBin(), open: openWithSystem}
}

// Open は単体ビューの本文のリンク (http / https の URL か、在る絶対パス) を既定のアプリで開く。
func (d *Daemon) Open(target string) error {
	if err := checkOpenTarget(target); err != nil {
		return err
	}
	return d.open(target)
}

// checkOpenTarget は開いてよい target かを確かめる。`open` はスキームでアプリを起こすので、本文 (セッションが書いた文字列)
// から渡せる URL を http と https に絞る。
func checkOpenTarget(target string) error {
	if filepath.IsAbs(target) {
		if _, err := os.Stat(target); err != nil {
			return fmt.Errorf("開けない: %w", err)
		}
		return nil
	}
	u, err := url.Parse(target)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("開けない: %q", target)
	}
	return nil
}

func openWithSystem(target string) error {
	ctx, cancel := context.WithTimeout(context.Background(), runTimeout)
	defer cancel()
	// target は checkOpenTarget を通った URL か絶対パスで、- で始まらない。
	out, err := exec.CommandContext(ctx, "/usr/bin/open", target).CombinedOutput() // #nosec G204 -- shell を通さず、target は引数 1 つとして渡る
	if err != nil {
		return fmt.Errorf("開けない: %s: %s", target, strings.TrimSpace(string(out)))
	}
	return nil
}

// Restore は切り替える前の窓の位置と大きさを戻し、画面の状態 (restore.js が詰めた JSON の object) を返す。
// 切り替えで起きたのでなければ空。1 度だけ返す。
func (d *Daemon) Restore() (string, error) {
	s := d.restored
	d.restored = nil
	if s == nil {
		return "", nil
	}
	if d.window != nil && s.Bounds.Width > 0 && s.Bounds.Height > 0 {
		d.window.SetBounds(s.Bounds)
	}
	b, err := json.Marshal(s.View)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// Upgrade は画面の状態 view を新しいバージョンへ渡し、新しい .app を起こしてから自分を終える。
func (d *Daemon) Upgrade(view string) error {
	if d.upgrader == nil || d.window == nil {
		return errors.New("切り替えの用意が無い")
	}
	if err := d.upgrader.switchTo(d.window.Bounds(), view); err != nil {
		return err
	}
	// 呼び出しへ返してから終える。
	go application.Get().Quit()
	return nil
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
	"work set":     runTimeout,
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
