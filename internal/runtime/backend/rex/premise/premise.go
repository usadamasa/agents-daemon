// Package premise は rex が control API に置いている前提 (応答の欄、対象が無いときの失敗、API revision) を、自分の
// block に固定の method を打って点検する。doctor だけが使い (multiplexer.PremiseChecker)、daemon の判定には関わらない。
// 欄の有無は json.Unmarshal のゼロ値では見分けられない (owner.pid が 0 と欄無し) ので、map で key の有無を見る。
package premise

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/usadamasa/agents-daemon/internal/multiplexer"
	"github.com/usadamasa/agents-daemon/internal/runtime/backend/rex/api"
	"github.com/usadamasa/agents-daemon/internal/runtime/backend/rex/wire"
)

// ProbeMissingBlock と ProbeMissingSession は存在しない block と session の ID。対象が無いときの失敗の形を見るためだけに
// 打つ (どれも読み取りで、rex の UI には残らない)。
const (
	ProbeMissingBlock   = "block:01a12599-0000-7000-8000-000000000001"
	ProbeMissingSession = "session:01a12599-0000-7000-8000-000000000002"
)

// describeMethods は rex が打つ block の method。
// rex-premise: preview.37 api session.describe_block は api.methods と api.events を method 名 / event 名を key にした object で返す
var describeMethods = []string{"program_status", "process", "size", "modes"}

// addFinding は点検 1 件を積む。
type addFinding func(ok bool, format string, a ...any)

// checker は 1 回の点検。
type checker struct {
	rex *api.Client
	add addFinding
}

// Check は自分の block に固定の method を打ち、応答の形が rex の読む形のままかを点検する。点検の失敗は error でなく
// OK=false の finding にする。locate は spawn の包みが叩く rex を引く (fork しない)。sessionID が空なら blockID から引く。
// 打つ method の一覧は .claude/skills/catching-up-with-rex-changelog/references/api-checks.md が持つ (method を足す・
// 変えるときはそちらも同じ PR で変える)。
func Check(ctx context.Context, rex *api.Client, locate func() (string, error), blockID, sessionID string) []multiplexer.PremiseFinding {
	var out []multiplexer.PremiseFinding
	c := checker{rex: rex, add: func(ok bool, format string, a ...any) {
		out = append(out, multiplexer.PremiseFinding{OK: ok, Text: fmt.Sprintf(format, a...)})
	}}
	c.checkVersion(ctx)
	c.checkWrapRex(locate)
	if sessionID == "" {
		var err error
		if sessionID, err = rex.ResolveBlock(ctx, blockID); err != nil {
			c.add(false, "自分の block (%s) の session を引けないため、block の点検を飛ばした: %v", blockID, err)
			c.checkMissingTarget(ctx, "")
			return out
		}
	}
	c.checkMissingTarget(ctx, sessionID)
	c.checkProgramStatus(ctx, sessionID, blockID)
	c.checkProcess(ctx, sessionID, blockID)
	c.checkListBlocks(ctx, sessionID, blockID)
	c.checkDescribe(ctx, sessionID, blockID)
	c.checkModes(ctx, sessionID, blockID)
	return out
}

// checkVersion は server の版を出し、server が対応する API revision の幅に daemon の revision が入るかを見る。
// changelog との突き合わせの起点 (last-checked.md の rex_version と built_at と api_revision)。幅を外れると hello が
// 断られ、daemon は rex を一切操作できない。
// rex-premise: preview.37 api server.version は rex_version、built_at、api_revision、api_min_revision (0 なら欄無し) を返す
func (c checker) checkVersion(ctx context.Context) {
	m, err := c.rex.Raw(ctx, "server.version", nil)
	if err != nil {
		c.add(false, "server.version が失敗: %v", err)
		return
	}
	if missing := missingPaths(m, "rex_version", "built_at"); len(missing) > 0 {
		c.add(false, "server.version に %s が無い", strings.Join(missing, " / "))
	} else {
		c.add(true, "rex_version: %v (built_at %v)", m["rex_version"], m["built_at"])
	}
	newest, has := revision(m, "api_revision")
	oldest, _ := revision(m, "api_min_revision")
	switch {
	case !has:
		c.add(false, "server.version に api_revision が無い (server が revision を名乗らない古い版。daemon は revision %d を話す)", api.Revision)
	case newest < api.Revision:
		c.add(false, "server の api_revision %d が daemon の revision %d より古い (rex を更新する)", newest, api.Revision)
	case oldest > api.Revision:
		c.add(false, "server の api_min_revision %d が daemon の revision %d より新しい (agents-daemon を更新する)", oldest, api.Revision)
	default:
		c.add(true, "daemon の api_revision %d は server の対応する幅 (%d から %d) に入る", api.Revision, oldest, newest)
	}
}

// revision は JSON の数の欄を int で返す。欄が無いか 0 なら has=false (server は 0 の欄を省く)。
func revision(m map[string]any, key string) (n int, has bool) {
	f, ok := m[key].(float64)
	if !ok || f == 0 {
		return 0, false
	}
	return int(f), true
}

// checkWrapRex は spawn の包みが叩く rex を引けるかを見て、引いたパスを出す。rex の自動更新は Versions/ の下に複数の
// rex を置くので、どれを引いたか (Current か数字名の build か、REX_BIN_PATH か) がパスで分かる (wrap.Locate)。
func (c checker) checkWrapRex(locate func() (string, error)) {
	bin, err := locate()
	if err != nil {
		c.add(false, "spawn の包みが叩く rex が見つからない (spawn した session が閉じた後に畳まれない): %v", err)
		return
	}
	c.add(true, "spawn の包みが叩く rex は %s", bin)
}

// checkMissingTarget は対象が無いときの失敗が api.IsNotFound の拾う形のままかを見る。PaneList が閉じた block と
// session を読み飛ばす判定がこれに乗っている。session が空なら block の method の点検を飛ばす。
func (c checker) checkMissingTarget(ctx context.Context, session string) {
	var bad []string
	if _, err := c.rex.ResolveBlock(ctx, ProbeMissingBlock); !api.IsNotFound(err) {
		bad = append(bad, fmt.Sprintf("存在しない block の target.resolve が not found でない (%v)", err))
	}
	if session != "" {
		if _, err := c.rex.RawBlock(ctx, session, ProbeMissingBlock, "program_status"); !api.IsNotFound(err) {
			bad = append(bad, fmt.Sprintf("存在しない block への block の method が not_found でない (%v)", err))
		}
	}
	if _, err := c.rex.ListBlocks(ctx, ProbeMissingSession); !api.IsNotFound(err) {
		bad = append(bad, fmt.Sprintf("存在しない session の list_blocks が session_not_found でない (%v)", err))
	}
	if len(bad) > 0 {
		c.add(false, "%s。閉じた block と session の読み飛ばしが効かない", strings.Join(bad, "、"))
		return
	}
	c.add(true, "無い block は not_found、無い session は session_not_found で返る (閉じた block と session を読み飛ばせる)")
}

// checkProgramStatus は自分の block の root record に wire.Record が読む欄があることを見る。
func (c checker) checkProgramStatus(ctx context.Context, session, blockID string) {
	m, err := c.rex.RawBlock(ctx, session, blockID, "program_status")
	if err != nil {
		c.add(false, "program_status が失敗: %v", err)
		return
	}
	root := rootRecordMap(m)
	if root == nil {
		c.add(false, "program_status に root record が無い (Claude Code の自己申告がまだ無いか、record の id の形が変わった)")
		return
	}
	if missing := missingPaths(root, wire.RecordFields...); len(missing) > 0 {
		c.add(false, "program_status の root record に %s が無い (wire.Record が読む欄。無いと agent_status が unknown のままになる)", strings.Join(missing, " / "))
		return
	}
	c.add(true, "program_status の root record に %s がある", strings.Join(wire.RecordFields, " / "))
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

// checkProcess は自分の block の process の foreground に wire.Foreground が読む欄があることを見る。
func (c checker) checkProcess(ctx context.Context, session, blockID string) {
	m, err := c.rex.RawBlock(ctx, session, blockID, "process")
	if err != nil {
		c.add(false, "process が失敗: %v", err)
		return
	}
	fg, ok := m["foreground"].(map[string]any)
	if !ok {
		c.add(false, "process に foreground が無い (前景の pid を取れず、record の owner と突き合わせられない)")
		return
	}
	if missing := missingPaths(fg, wire.ForegroundFields...); len(missing) > 0 {
		c.add(false, "process の foreground に %s が無い (wire.Foreground が読む欄)", strings.Join(missing, " / "))
		return
	}
	c.add(true, "process の foreground に %s がある", strings.Join(wire.ForegroundFields, " / "))
}

// checkListBlocks は自分の session の list_blocks に自分の block があり、flavor が shell であることを見る。
// PaneList はこの flavor の block にだけ program_status を打つ。
func (c checker) checkListBlocks(ctx context.Context, session, blockID string) {
	blocks, err := c.rex.ListBlocks(ctx, session)
	if err != nil {
		c.add(false, "session.list_blocks が失敗: %v", err)
		return
	}
	for _, b := range blocks.Blocks {
		if b.BlockID != blockID {
			continue
		}
		if b.Flavor != wire.ShellFlavor {
			c.add(false, "自分の block の flavor が %q (コードは %q を shell と見る。違うと PaneList が全 block を読み飛ばす)", b.Flavor, wire.ShellFlavor)
			return
		}
		c.add(true, "session.list_blocks に自分の block があり flavor は %s", wire.ShellFlavor)
		return
	}
	c.add(false, "session.list_blocks に自分の block (%s) が無い", blockID)
}

// checkDescribe は block の api にコードが打つ method と、購読が読む block の event があることを見る。
func (c checker) checkDescribe(ctx context.Context, session, blockID string) {
	m, err := c.rex.Raw(ctx, "session.describe_block", map[string]any{"session_id": session, "block_id": blockID})
	if err != nil {
		c.add(false, "session.describe_block が失敗: %v", err)
		return
	}
	var missing []string
	for _, name := range describeMethods {
		if !hasPath(m, "api.methods."+name) {
			missing = append(missing, "method "+name)
		}
	}
	for _, name := range wire.BlockEvents {
		if !hasPath(m, "api.events."+name) {
			missing = append(missing, "event "+name)
		}
	}
	if len(missing) > 0 {
		c.add(false, "describe_block の api に %s が無い", strings.Join(missing, " / "))
		return
	}
	c.add(true, "describe_block の api に method %s と event %s がある",
		strings.Join(describeMethods, " / "), strings.Join(wire.BlockEvents, " / "))
}

// checkModes は自分の block (doctor を Bash ツールから呼んだ Claude Code の block) が alternate screen にいることを見る。
// format が可視の行しか返さず、block の高さで読める行が頭打ちになる前提 (program-status.md「画面のモード」) は、この screen で描いて
// いることに乗っている。primary なら Claude Code の画面でない (起動直後の dialog、/exit の後の shell)。
func (c checker) checkModes(ctx context.Context, session, blockID string) {
	m, err := c.rex.RawBlock(ctx, session, blockID, "modes")
	if err != nil {
		c.add(false, "modes が失敗 (block の screen を見られず、doctor の pane 一覧に高さと screen が出ない): %v", err)
		return
	}
	screen, _ := m["screen"].(string)
	if screen != wire.ScreenAlternate {
		c.add(false, "自分の block の screen が %q (Claude Code は alternate screen で描く前提。起動直後の dialog か、Claude Code の画面でない)", screen)
		return
	}
	c.add(true, "自分の block の screen は alternate (format は可視の行だけを返す)")
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
