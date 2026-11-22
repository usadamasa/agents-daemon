package releaseverify

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/usadamasa/agents-daemon/tools/release-verify/releaseverify/command"
	"github.com/usadamasa/agents-daemon/tools/release-verify/releaseverify/record"
)

// stateDirs は複製する state。daemon の動作の引き金になるもの (spawned-sessions、rate-limits、quota-resume、
// turn-failures) と daemon 自身のファイルは写さない。
var stateDirs = []string{"work", "tasks", "edges", "parking", "refs", "sessions", "context", "dream"}

// up は環境を作る。commit を展開して建て、実データを複製する。
func (e *environment) up(ctx context.Context) error {
	if _, err := os.Lstat(e.root); err == nil {
		return refuse("同じ tree の環境が残っている: %s (down で消してから打つ)", e.root)
	}
	if err := e.refuseOverlap(); err != nil {
		return err
	}
	if err := os.MkdirAll(e.cfg.Base, 0o700); err != nil {
		return err
	}
	// 同じ tree の環境を別の呼び出しが作っていたら、ここで落ちる (MkdirAll にしない)。
	if err := os.Mkdir(e.root, 0o700); err != nil {
		return err
	}
	if err := e.writeMarker(); err != nil {
		return err
	}
	src := filepath.Join(e.root, "src")
	for _, dir := range []string{src, filepath.Join(e.root, "tmp"), e.report, filepath.Dir(e.bin)} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	if err := os.WriteFile(filepath.Join(e.root, "fingerprint.before"), []byte(e.fingerprint()), 0o600); err != nil {
		return err
	}

	e.info("commit %s (tree %s) を展開して建てる: %s", e.commit[:12], e.tree[:12], e.root)
	if err := e.extract(ctx, src); err != nil {
		return err
	}
	err := command.RunHost(ctx, "go", "build", "-C", src, "-ldflags", "-X main.version="+e.version(), "-o", e.bin, "./cmd/agents-daemon")
	if err != nil {
		return err
	}
	if e.cfg.BuildApp {
		if err := e.buildApp(ctx, src); err != nil {
			return err
		}
	} else {
		e.info("アプリは建てない")
	}

	e.info("実データを複製する")
	if err := e.snapshot(ctx); err != nil {
		return err
	}
	e.info("環境を作った: %s", e.root)
	return nil
}

func (e *environment) writeMarker() error {
	data, err := json.Marshal(marker{Schema: 1, Commit: e.commit, Tree: e.tree, CreatedAt: record.Now()})
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(e.root, markerName), append(data, '\n'), 0o600)
}

// extract は commit の中身を dst に展開する。作業ツリーの未 commit の変更は入らない。
func (e *environment) extract(ctx context.Context, dst string) error {
	archive := command.Host(ctx, "git", "-C", e.cfg.Repo, "archive", e.commit)
	untar := command.Host(ctx, "tar", "-x", "-C", dst)
	pipe, err := archive.StdoutPipe()
	if err != nil {
		return err
	}
	untar.Stdin = pipe
	// 2 つの process の stderr を同じ writer に繋ぐと書き込みが競合するので、別々に受ける。
	var archiveErr, untarErr bytes.Buffer
	archive.Stderr, untar.Stderr = &archiveErr, &untarErr
	if err := archive.Start(); err != nil {
		return err
	}
	untarResult := untar.Run()
	if err := archive.Wait(); err != nil {
		return fmt.Errorf("git archive: %w\n%s", err, archiveErr.String())
	}
	if untarResult != nil {
		return fmt.Errorf("tar: %w\n%s", untarResult, untarErr.String())
	}
	return nil
}

// buildApp は依存図のアプリを bundle に包んで建てる (IME と Info.plist の設定が素のバイナリでは本番と揃わない)。
// bundle id は常用のアプリと分け、設定の保存先を共有させない。
func (e *environment) buildApp(ctx context.Context, src string) error {
	contents := filepath.Join(e.app, "Contents")
	for _, dir := range []string{filepath.Join(contents, "MacOS"), filepath.Join(contents, "Resources")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	plist := filepath.Join(contents, "Info.plist")
	gui := filepath.Join(src, "gui")
	build := command.Host(ctx, "go", "build", "-C", gui, "-ldflags", "-X main.version="+e.version(), "-o", e.appBinary(), ".")
	// deployment target は Taskfile の gui:build と同じ。
	build.Env = append(build.Env, "CGO_CFLAGS=-mmacosx-version-min=13.0", "CGO_LDFLAGS=-mmacosx-version-min=13.0",
		"MACOSX_DEPLOYMENT_TARGET=13.0")
	if out, err := build.CombinedOutput(); err != nil {
		return fmt.Errorf("アプリの build: %w\n%s", err, out)
	}
	steps := [][]string{
		{"cp", filepath.Join(gui, "darwin", "agent-tasks.icns"), filepath.Join(contents, "Resources", "agent-tasks.icns")},
		{"cp", filepath.Join(gui, "darwin", "Info.plist"), plist},
		{"plutil", "-replace", "CFBundleShortVersionString", "-string", e.version(), plist},
		{"plutil", "-replace", "CFBundleVersion", "-string", e.version(), plist},
		{"plutil", "-replace", "CFBundleIdentifier", "-string", "com.github.usadamasa.agents-daemon.agent-tasks.verify", plist},
		{"codesign", "--remove-signature", e.appBinary()},
		{"codesign", "--force", "--sign", "-", e.app},
	}
	for _, step := range steps {
		if err := command.RunHost(ctx, step[0], step[1:]...); err != nil {
			return err
		}
	}
	return nil
}

// fingerprint は、常用の環境のうち、この道具が変えてはいけないファイルの中身の要約を出す。
func (e *environment) fingerprint() string {
	binDir := filepath.Dir(e.cfg.BaselineBin)
	files := []string{
		e.cfg.BaselineBin,
		filepath.Join(binDir, "agents-daemon.version"),
		filepath.Join(binDir, "agent-tasks.version"),
		filepath.Join(e.cfg.RealApp, "Contents", "Info.plist"),
		filepath.Join(e.cfg.RealApp, "Contents", "MacOS", "agent-tasks"),
		e.cfg.SourceConfig,
	}
	var b strings.Builder
	for _, f := range files {
		// #nosec G304 -- 読むのは設定で決まる常用の環境のファイル
		data, err := os.ReadFile(f)
		if err != nil {
			fmt.Fprintf(&b, "absent %s\n", f)
			continue
		}
		fmt.Fprintf(&b, "%x %s\n", sha256.Sum256(data), f)
	}
	return b.String()
}

// snapshot は実データを allowlist で複製する。transcript と、allowlist に無い state は写さない。
func (e *environment) snapshot(ctx context.Context) error {
	for _, dir := range []string{e.state, filepath.Join(e.claude, "projects"), filepath.Join(e.claude, "plugins"),
		filepath.Join(e.home, ".config", "agents-daemon")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	copies := map[string]string{}
	for _, name := range stateDirs {
		copies[filepath.Join(e.cfg.SourceState, name)] = filepath.Join(e.state, name)
	}
	for _, name := range []string{"CLAUDE.md", "skills"} {
		copies[filepath.Join(e.cfg.SourceClaude, name)] = filepath.Join(e.claude, name)
	}
	memories, err := filepath.Glob(filepath.Join(e.cfg.SourceClaude, "projects", "*", "memory"))
	if err != nil {
		return err
	}
	for _, memory := range memories {
		project := filepath.Base(filepath.Dir(memory))
		copies[memory] = filepath.Join(e.claude, "projects", project, "memory")
	}
	copies[filepath.Join(e.cfg.SourceClaude, "plugins", "installed_plugins.json")] =
		filepath.Join(e.claude, "plugins", "installed_plugins.json")
	copies[e.cfg.SourceConfig] = filepath.Join(e.home, ".config", "agents-daemon", "config.json")

	for src, dst := range copies {
		if _, err := os.Lstat(src); err != nil {
			continue
		}
		if err := e.copyTree(ctx, src, dst); err != nil {
			return err
		}
	}
	return removeTransient(e.state)
}

// copyTree は src を dst へ写す。cp は mtime を保ち (-p。カードの状態と保持期間は mtime から導く)、symlink を辿らない
// (-P。辿ると skill の実体の配置が本番と変わる)。常用の daemon が rename で置き換えた瞬間に当たると cp が落ちるので、
// 3 回まで取り直す。
func (e *environment) copyTree(ctx context.Context, src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	var err error
	for i := 1; i <= 3; i++ {
		if err = os.RemoveAll(dst); err != nil {
			return err
		}
		if err = command.RunHost(ctx, "cp", "-RPp", src, dst); err == nil {
			return nil
		}
		e.info("複製を取り直す (%d 回目): %s", i, src)
	}
	return fmt.Errorf("複製できない: %s: %w", src, err)
}

// removeTransient は、複製に紛れた lock と書きかけのファイルを消す。
func removeTransient(dir string) error {
	// 複製には symlink が入るので、dir の外へ出ない root 越しに消す。
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	return fs.WalkDir(root.FS(), ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		name := d.Name()
		if name == ".lock" || (strings.HasPrefix(name, ".") && strings.HasSuffix(name, ".tmp")) {
			if err := root.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
		return nil
	})
}
