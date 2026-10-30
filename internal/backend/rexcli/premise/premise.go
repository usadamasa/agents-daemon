// Package premise は rexcli が rex の CLI に置いている前提 (応答の欄、exit code、flag) を、自分の block に固定の
// コマンドを打って点検する。doctor だけが使い (multiplexer.PremiseChecker)、daemon の判定には関わらない。
// 欄の有無は json.Unmarshal のゼロ値では見分けられない (owner.pid が 0 と欄無し) ので、map で key の有無を見る。
package premise

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/usadamasa/agents-daemon/internal/backend/rexcli/blockstate"
	"github.com/usadamasa/agents-daemon/internal/multiplexer"
)

// ProbeMissingBlock は存在しない block の ID。exit code と stderr の形を見るためだけに打つ
// (block target not found は client 側のエラーで、rex のログや UI には残らない)。
const ProbeMissingBlock = "block:01a12599-0000-7000-8000-000000000001"

// inspectMethods は rexcli が打つ block の method。
// rex-premise: preview.22 cli block inspect --json は api.methods と api.events を method 名 / event 名を key にした object で返す
var inspectMethods = []string{"program_status", "process", "size", "modes"}

// Runner は rex を 1 回呼ぶ口。実装は rexcli の Client で、同じ呼び方 (--autostart=false、REX_SESSION を外した環境) で打つ。
type Runner interface {
	// Run は rex に args を渡して stdout を返す。失敗は stderr を添えた error。
	Run(ctx context.Context, args ...string) ([]byte, error)
	// TargetNotFound は Run の error が「対象を解決できない」(exit 3) かを返す。
	TargetNotFound(err error) bool
	// BinPath は呼んだ rex の絶対パス。
	BinPath() string
}

// addFinding は点検 1 件を積む。
type addFinding func(ok bool, format string, a ...any)

// checker は 1 回の点検。
type checker struct {
	r Runner
}

// Check は自分の block に固定のコマンドを打ち、応答の形が rexcli の読む形のままかを点検する。点検の失敗は error でなく
// OK=false の finding にする。打つコマンドの一覧は .claude/skills/catching-up-with-rex-changelog/references/cli-checks.md
// が持つ (コマンドを足す・変えるときはそちらも同じ PR で変える)。
func Check(ctx context.Context, r Runner, blockID, sessionID string) []multiplexer.PremiseFinding {
	c := checker{r: r}
	var out []multiplexer.PremiseFinding
	add := func(ok bool, format string, a ...any) {
		out = append(out, multiplexer.PremiseFinding{OK: ok, Text: fmt.Sprintf(format, a...)})
	}
	c.checkVersion(ctx, add)
	c.checkMissingTarget(ctx, add)
	c.checkProgramStatus(ctx, blockID, add)
	c.checkProcess(ctx, blockID, add)
	c.checkBlockLs(ctx, blockID, sessionID, add)
	c.checkInspect(ctx, blockID, add)
	c.checkModes(ctx, blockID, add)
	return out
}

// checkModes は自分の block (doctor を Bash ツールから呼んだ Claude Code の block) が alternate screen にいることを見る。
// capture が可視の行しか返さず、block の高さで検知が頭打ちになる前提 (pane-io.md) は、この screen で描いて
// いることに乗っている。primary なら Claude Code の画面でない (起動直後の dialog、/exit の後の shell)。
func (c checker) checkModes(ctx context.Context, blockID string, add addFinding) {
	m, err := c.runMap(ctx, "block", "call", "-b", blockID, "modes")
	if err != nil {
		add(false, "modes が失敗 (block の screen を見られず、doctor の pane 一覧に高さと screen が出ない): %v", err)
		return
	}
	screen, _ := m["screen"].(string)
	if screen != blockstate.ScreenAlternate {
		add(false, "自分の block の screen が %q (Claude Code は alternate screen で描く前提。起動直後の dialog か、Claude Code の画面でない)", screen)
		return
	}
	add(true, "自分の block の screen は alternate (capture は可視の行だけを返す)")
}

// checkVersion は rex の版を出す。changelog との突き合わせの起点 (last-checked.md の rex_version と built_at と
// api_revision)。api_revision は CLI が対応する API の版で、あるときだけ出して server が動かしている版
// (api call server.status の api_revision) と比べる。CLI と server は別々に更新されるので、食い違いは
// daemon が古い CLI で新しい server を叩いている (またはその逆) 状態。
// 版と並べて解決した binary のパスも出す。rex の自動更新は Versions/ の下に複数の rex を置くので、
// どれを引いたか (Current か数字名の build か、REX_BIN_PATH か) がパスで分かる (rexcli の locate.go)。
// rex-premise: preview.22 cli version --json は rex_version と built_at と api_revision を返す
// rex-premise: preview.22 cli api call server.status は api_revision を返す (CLI と同じ値)
func (c checker) checkVersion(ctx context.Context, add addFinding) {
	m, err := c.runMap(ctx, "version", "--json")
	if err != nil {
		add(false, "version --json が失敗: %v", err)
		return
	}
	if missing := missingPaths(m, "rex_version", "built_at"); len(missing) > 0 {
		add(false, "version --json に %s が無い", strings.Join(missing, " / "))
		return
	}
	cliRevision, has := m["api_revision"]
	if !has {
		add(true, "rex_version: %v (built_at %v)。呼んだ rex は %s", m["rex_version"], m["built_at"], c.r.BinPath())
		return
	}
	add(true, "rex_version: %v (built_at %v, api_revision %v)。呼んだ rex は %s", m["rex_version"], m["built_at"], cliRevision, c.r.BinPath())
	c.checkServerAPIRevision(ctx, cliRevision, add)
}

// checkServerAPIRevision は server の api_revision が CLI の cliRevision と同じかを見る。
func (c checker) checkServerAPIRevision(ctx context.Context, cliRevision any, add addFinding) {
	st, err := c.runMap(ctx, "api", "call", "server.status")
	if err != nil {
		add(false, "api call server.status が失敗 (server の api_revision を比べられない): %v", err)
		return
	}
	serverRevision, has := st["api_revision"]
	switch {
	case !has:
		add(false, "server status に api_revision が無い (CLI は %v。server が CLI より古い版で動いている)", cliRevision)
	case serverRevision != cliRevision:
		add(false, "api_revision が CLI (%v) と server (%v) で食い違う (片方だけ更新された。daemon は CLI の版で server を叩く)", cliRevision, serverRevision)
	default:
		add(true, "api_revision は CLI と server で同じ (%v)", cliRevision)
	}
}

// checkMissingTarget は存在しない block への block call が exit 3 で、stderr に not found を含むことを見る。
// PaneList が閉じた block を読み飛ばす判定 (Runner.TargetNotFound) がこれに乗っている。
func (c checker) checkMissingTarget(ctx context.Context, add addFinding) {
	_, err := c.r.Run(ctx, "block", "call", "-b", ProbeMissingBlock, "program_status")
	switch {
	case err == nil:
		add(false, "存在しない block への block call が成功した (exit 3 を期待。閉じた block の読み飛ばしが効かない)")
	case !c.r.TargetNotFound(err) || !strings.Contains(err.Error(), "not found"):
		add(false, "存在しない block への block call が exit 3 + not found でない: %v", err)
	default:
		add(true, "存在しない block への block call は exit 3 で stderr に not found")
	}
}

// checkProgramStatus は自分の block の root record に blockstate.Record が読む欄があることを見る。
func (c checker) checkProgramStatus(ctx context.Context, blockID string, add addFinding) {
	m, err := c.runMap(ctx, "block", "call", "-b", blockID, "program_status")
	if err != nil {
		add(false, "program_status が失敗: %v", err)
		return
	}
	root := rootRecordMap(m)
	if root == nil {
		add(false, "program_status に root record が無い (Claude Code の自己申告がまだ無いか、record の id の形が変わった)")
		return
	}
	if missing := missingPaths(root, blockstate.RecordFields...); len(missing) > 0 {
		add(false, "program_status の root record に %s が無い (blockstate.Record が読む欄。無いと agent_status が unknown のままになる)", strings.Join(missing, " / "))
		return
	}
	add(true, "program_status の root record に %s がある", strings.Join(blockstate.RecordFields, " / "))
}

// rootRecordMap は records の中から subagent でない record (id が無いか空) を返す。
func rootRecordMap(m map[string]any) map[string]any {
	records, _ := m["records"].([]any)
	for _, r := range records {
		rec, ok := r.(map[string]any)
		if !ok {
			continue
		}
		if id, has := rec["id"]; !has || id == "" {
			return rec
		}
	}
	return nil
}

// checkProcess は自分の block の process の foreground に blockstate.Foreground が読む欄があることを見る。
func (c checker) checkProcess(ctx context.Context, blockID string, add addFinding) {
	m, err := c.runMap(ctx, "block", "call", "-b", blockID, "process")
	if err != nil {
		add(false, "process が失敗: %v", err)
		return
	}
	fg, ok := m["foreground"].(map[string]any)
	if !ok {
		add(false, "process に foreground が無い (前景の pid を取れず、record の owner と突き合わせられない)")
		return
	}
	if missing := missingPaths(fg, blockstate.ForegroundFields...); len(missing) > 0 {
		add(false, "process の foreground に %s が無い (blockstate.Foreground が読む欄)", strings.Join(missing, " / "))
		return
	}
	add(true, "process の foreground に %s がある", strings.Join(blockstate.ForegroundFields, " / "))
}

// checkBlockLs は自分の session の block ls に自分の block があり、flavor が shell であることを見る。
// PaneList はこの flavor の block にだけ program_status を打つ。
func (c checker) checkBlockLs(ctx context.Context, blockID, sessionID string, add addFinding) {
	if sessionID == "" {
		add(false, "session が分からないため block ls の点検を飛ばした (REX_SESSION を持つ block の中から呼ぶ)")
		return
	}
	var blocks blockstate.BlockList
	if err := c.runJSON(ctx, &blocks, "block", "ls", "-s", sessionID, "--json"); err != nil {
		add(false, "block ls -s が失敗: %v", err)
		return
	}
	for _, b := range blocks.Blocks {
		if b.BlockID != blockID {
			continue
		}
		if b.Flavor != blockstate.ShellFlavor {
			add(false, "自分の block の flavor が %q (コードは %q を shell と見る。違うと PaneList が全 block を読み飛ばす)", b.Flavor, blockstate.ShellFlavor)
			return
		}
		add(true, "block ls -s に自分の block があり flavor は %s", blockstate.ShellFlavor)
		return
	}
	add(false, "block ls -s に自分の block (%s) が無い", blockID)
}

// checkInspect は block の api にコードが打つ method と、購読が読む block の event があることを見る。
func (c checker) checkInspect(ctx context.Context, blockID string, add addFinding) {
	m, err := c.runMap(ctx, "block", "inspect", blockID, "--json")
	if err != nil {
		add(false, "block inspect --json が失敗: %v", err)
		return
	}
	var missing []string
	for _, name := range inspectMethods {
		if !hasPath(m, "api.methods."+name) {
			missing = append(missing, "method "+name)
		}
	}
	for _, name := range blockstate.BlockEvents {
		if !hasPath(m, "api.events."+name) {
			missing = append(missing, "event "+name)
		}
	}
	if len(missing) > 0 {
		add(false, "block inspect の api に %s が無い", strings.Join(missing, " / "))
		return
	}
	add(true, "block inspect の api に method %s と event %s がある",
		strings.Join(inspectMethods, " / "), strings.Join(blockstate.BlockEvents, " / "))
}

// runJSON は rex を実行し stdout の JSON を v へ読む。
func (c checker) runJSON(ctx context.Context, v any, args ...string) error {
	out, err := c.r.Run(ctx, args...)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(out, v); err != nil {
		return fmt.Errorf("rex %v: 応答の parse に失敗: %w (stdout=%q)", args, err, out)
	}
	return nil
}

// runMap は応答の JSON を key の有無を見られる形で読む。
func (c checker) runMap(ctx context.Context, args ...string) (map[string]any, error) {
	var m map[string]any
	if err := c.runJSON(ctx, &m, args...); err != nil {
		return nil, err
	}
	return m, nil
}

// hasPath は dotted path の key が全部あるかを返す。途中が object でなければ無いと見る。
func hasPath(m map[string]any, path string) bool {
	cur := any(m)
	for _, key := range strings.Split(path, ".") {
		obj, ok := cur.(map[string]any)
		if !ok {
			return false
		}
		if cur, ok = obj[key]; !ok {
			return false
		}
	}
	return true
}

// missingPaths は paths のうち m に無いものを返す。
func missingPaths(m map[string]any, paths ...string) []string {
	var missing []string
	for _, p := range paths {
		if !hasPath(m, p) {
			missing = append(missing, p)
		}
	}
	sort.Strings(missing)
	return missing
}
