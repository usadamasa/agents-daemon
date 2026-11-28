package releaseverify

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/usadamasa/agents-daemon/tools/release-verify/releaseverify/command"
	"github.com/usadamasa/agents-daemon/tools/release-verify/releaseverify/record"
)

// markerName は、この道具が作った環境の root に置く印。down はこの印のあるディレクトリだけを消す。
const markerName = ".agents-daemon-verify"

// environment は、ref から決まる 1 つの検証の環境。
type environment struct {
	cfg    Config
	commit string
	tree   string
	// root の下に src (commit の展開)、home (隔離の HOME)、report、tmp を置く。
	root   string
	home   string
	state  string
	claude string
	bin    string
	app    string
	report string
}

type marker struct {
	Schema    int    `json:"schema"`
	Commit    string `json:"commit"`
	Tree      string `json:"tree"`
	CreatedAt string `json:"created_at"`
}

func resolve(ctx context.Context, cfg Config, ref string) (*environment, error) {
	commit, err := command.GitOutput(ctx, cfg.Repo, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	if err != nil {
		return nil, refuse("ref を commit に解決できない: %s", ref)
	}
	tree, err := command.GitOutput(ctx, cfg.Repo, "rev-parse", "--verify", commit+"^{tree}")
	if err != nil {
		return nil, err
	}
	root := filepath.Join(cfg.Base, tree[:12])
	home := filepath.Join(root, "home")
	return &environment{
		cfg: cfg, commit: commit, tree: tree, root: root, home: home,
		state:  filepath.Join(home, ".local", "state", "agents-daemon"),
		claude: filepath.Join(home, ".claude"),
		bin:    filepath.Join(home, ".cache", "agents-daemon", "bin", "agents-daemon"),
		app:    filepath.Join(home, "Applications", "agent-tasks.app"),
		report: filepath.Join(root, "report"),
	}, nil
}

func (e *environment) info(format string, args ...any) {
	_, _ = fmt.Fprintf(e.cfg.Stderr, "release-verify: %s\n", fmt.Sprintf(format, args...))
}

// record は、この tree の記録を付ける先。
func (e *environment) record() record.Target {
	return record.Target{Dir: filepath.Join(e.cfg.Base, "records"), Repo: e.cfg.Repo, Commit: e.commit, Tree: e.tree}
}

// version は建てるバイナリとアプリに埋めるバージョン。
func (e *environment) version() string {
	return "verify-" + e.commit[:7]
}

func (e *environment) appBinary() string {
	return filepath.Join(e.app, "Contents", "MacOS", "agent-tasks")
}

// require は、root がこの道具の作った、ref と同じ tree の環境であることを確かめる。消す前と、中のバイナリを動かす前に呼ぶ。
func (e *environment) require() error {
	info, err := os.Lstat(e.root)
	if err == nil && info.Mode()&os.ModeSymlink != 0 {
		return refuse("環境の root が symlink になっている: %s", e.root)
	}
	if err != nil || !info.IsDir() {
		return refuse("環境が無い: %s (先に up を打つ)", e.root)
	}
	data, err := os.ReadFile(filepath.Join(e.root, markerName))
	if err != nil {
		return refuse("印 (%s) が無いので、この道具が作った環境ではない: %s", markerName, e.root)
	}
	if physical(filepath.Dir(e.root)) != physical(e.cfg.Base) {
		return refuse("環境の root が置き場の外を指している: %s", e.root)
	}
	var m marker
	if err := json.Unmarshal(data, &m); err != nil || m.Tree != e.tree {
		return refuse("印の tree が ref の tree と違う: %s", e.root)
	}
	return nil
}

// refuseOverlap は、複製元と環境の置き場が重なっていれば拒む。重なると、down が原本を消す。
func (e *environment) refuseOverlap() error {
	base := physical(e.cfg.Base)
	for _, src := range []string{e.cfg.SourceState, e.cfg.SourceClaude, filepath.Dir(e.cfg.SourceConfig)} {
		if _, err := os.Stat(src); err != nil {
			continue
		}
		src = physical(src)
		if within(src, base) {
			return refuse("複製元が環境の置き場の下にある: %s", src)
		}
		if within(base, src) {
			return refuse("環境の置き場が複製元の下にある: %s", base)
		}
	}
	return nil
}

// within は path が dir と同じか、その下にあるかを返す。
func within(path, dir string) bool {
	sep := string(filepath.Separator)
	return strings.HasPrefix(path+sep, dir+sep)
}

// physical は symlink を解いたパスを返す。まだ無い末尾は、在る祖先を解いた先に付け直す。
func physical(path string) string {
	rest := ""
	for {
		if resolved, err := filepath.EvalSymlinks(path); err == nil {
			return filepath.Join(resolved, rest)
		}
		parent := filepath.Dir(path)
		if parent == path {
			return filepath.Join(path, rest)
		}
		rest = filepath.Join(filepath.Base(path), rest)
		path = parent
	}
}

// commonEnv は隔離の env の共通の部分。HOME と XDG の 3 つを環境の下へ向ける。
func (e *environment) commonEnv() []string {
	lang := os.Getenv("LANG")
	if lang == "" {
		lang = "en_US.UTF-8"
	}
	return []string{
		"HOME=" + e.home,
		"CFFIXED_USER_HOME=" + e.home,
		"XDG_STATE_HOME=" + filepath.Join(e.home, ".local", "state"),
		"XDG_CACHE_HOME=" + filepath.Join(e.home, ".cache"),
		"XDG_CONFIG_HOME=" + filepath.Join(e.home, ".config"),
		"PATH=/usr/bin:/bin:/usr/sbin:/sbin",
		"LANG=" + lang,
		"TMPDIR=" + filepath.Join(e.root, "tmp"),
	}
}

// cliEnv は CLI とアプリの env。rex は存在しない socket、Claude の設定ディレクトリは複製を指す。
func (e *environment) cliEnv() []string {
	return append(e.commonEnv(),
		"CLAUDE_CONFIG_DIR="+e.claude,
		"REX_SERVER=unix://"+filepath.Join(e.root, "rex-dead.sock"),
	)
}

// daemonEnv は daemon の env。本物の rex と Claude の設定ディレクトリを指す。
// REX_SERVER は URL なので空白を percent-encode する。REX_BLOCK は起動の条件で、ループは値を読まない。
// rex-premise: preview.37 api REX_SERVER は unix:// の URL。既定の socket は ~/Library/Application Support/rex/server.sock
func (e *environment) daemonEnv() []string {
	return append(e.commonEnv(),
		"CLAUDE_CONFIG_DIR="+e.cfg.SourceClaude,
		"REX_SERVER=unix://"+strings.ReplaceAll(e.cfg.RexSocket, " ", "%20"),
		"REX_BLOCK=block:release-verify",
	)
}
