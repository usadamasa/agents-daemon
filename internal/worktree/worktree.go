// Package worktree は Claude Code が作った worktree のうち、もう誰も使っていないものを回収する。
//
// Claude Code は `<main worktree>/.claude/worktrees/<name>` に worktree を作り、使用中は
// `git worktree lock --reason "claude session <name> (pid <PID> ...)"` で押さえる。セッション終了時の削除は
// 「keep / remove」の選択に委ねられていて、keep を選ぶとそのまま残る。本体の periodic sweep が消すのは subagent と
// background session の worktree だけで、foreground セッションが keep したものは年齢に関わらず残り続ける。
// このパッケージはその残りを、SessionStart のたびに今開いたリポジトリの分だけ回収する。
//
// cc-premise: 2.1.310 worktrees 本体の sweep は subagent / background session の worktree と exit 済みプロセスの stale lock だけを扱い、foreground の keep は残す (docs/worktrees)
//
// 回収する条件 (すべて満たすとき):
//  1. パスが `<main worktree>/.claude/worktrees/` 配下
//  2. lock されていない、または foreground セッションの lock でその PID が不在
//     (稼働中セッションの lock、人手の lock、subagent の lock には触らない)
//  3. 最終更新から MinAge 以上経過している
//  4. working tree に未コミット・未追跡の変更が無い (Force で無視)
//
// ディレクトリが既に消えている登録だけの残骸は 3, 4 を問わず回収する。ブランチは消さない
// (`git worktree remove` はブランチを残すので、コミット済みの作業は push 前でも失われない)。
package worktree

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// DefaultMinAge は回収の既定のしきい値。gitignore されたローカルファイル (.env など) は remove で失われるので、
// 「1 週間触っていなければ戻って来ない」とみなせる長さにしてある。
const DefaultMinAge = 7 * 24 * time.Hour

// ErrNotRepository は Cwd が git リポジトリの中でないときの Run の返り値。
var ErrNotRepository = errors.New("git リポジトリの中で実行してください")

// Action は 1 つの worktree をどうしたか。
type Action string

const (
	// ActionRemoved は回収したことを表す。
	ActionRemoved Action = "removed"
	// ActionWouldRemove は DryRun で回収対象と判定したことを表す。
	ActionWouldRemove Action = "would_remove"
	// ActionKept は回収の条件を満たさず残したことを表す。
	ActionKept Action = "kept"
)

// Reason は Action の理由。
type Reason string

const (
	// ReasonStale は最終更新から MinAge 以上経っていることを表す。
	ReasonStale Reason = "stale"
	// ReasonMissing は実体が無く登録だけ残っていることを表す。
	ReasonMissing Reason = "missing"
	// ReasonDirty は未コミットの変更があることを表す (Force で無視)。
	ReasonDirty Reason = "dirty"
	// ReasonForeignLock は Claude Code 以外の lock があることを表す。
	ReasonForeignLock Reason = "foreign_lock"
)

// Decision は文字で報告した 1 つの worktree の判定。黙って飛ばすもの (新しい、生きているセッションの lock、
// subagent の lock、範囲外) は載らない。
type Decision struct {
	Path    string `json:"path"`
	Action  Action `json:"action"`
	Reason  Reason `json:"reason"`
	AgeDays int    `json:"age_days,omitempty"`
	// Lock は ReasonForeignLock のときの lock の理由。
	Lock string `json:"lock,omitempty"`
}

// Result は 1 回の Run の判定。`prune-worktrees --json` はこれをそのまま出す。
type Result struct {
	Worktrees []Decision `json:"worktrees"`
}

// Opts は Run の入力。
type Opts struct {
	Cwd    string             // 起点。ここから main worktree と「自分がいる worktree」を解決する
	MinAge time.Duration      // この時間以上触っていない worktree だけ回収する
	Force  bool               // 未コミットの変更がある worktree も削除する
	DryRun bool               // 削除せず対象だけ表示する
	Now    time.Time          // 零値なら time.Now()
	Alive  func(pid int) bool // 零値なら processAlive
	Stdout io.Writer          // 回収した・残した理由の報告先。何もしなければ何も書かない
}

// entry は `git worktree list --porcelain` の 1 ブロック。
type entry struct {
	Path   string
	Locked bool
	Reason string // Locked で理由が無ければ空
}

// pruner は 1 回の Run の文脈。
type pruner struct {
	opts   Opts
	main   string // main worktree (削除操作の起点)
	self   string // 自分がいる worktree。判定によらず消さない
	root   string // <main>/.claude/worktrees
	now    time.Time
	cutoff time.Time
	result Result
}

// Run は opts.Cwd のリポジトリで回収を 1 回行う。個々の worktree の git 操作の失敗はその場で返し、そこまでの判定は
// Result に残す。
func Run(opts Opts) (Result, error) {
	p, err := newPruner(opts)
	if err != nil {
		return Result{Worktrees: []Decision{}}, err
	}
	entries, err := listWorktrees(p.main)
	if err != nil {
		return p.result, err
	}
	for _, e := range entries {
		if err := p.handle(e); err != nil {
			return p.result, err
		}
	}
	return p.result, nil
}

func newPruner(opts Opts) (*pruner, error) {
	if opts.Now.IsZero() {
		opts.Now = time.Now()
	}
	if opts.Alive == nil {
		opts.Alive = processAlive
	}
	if opts.Stdout == nil {
		opts.Stdout = io.Discard
	}
	main, err := mainWorktree(opts.Cwd)
	if err != nil {
		return nil, err
	}
	// 自分が中にいる worktree は判定によらず消さない。誤判定したときに実行中の作業ディレクトリごと消さないための保険。
	self := ""
	if top, err := runGit(opts.Cwd, "rev-parse", "--show-toplevel"); err == nil {
		self = canon(top)
	}
	return &pruner{
		opts:   opts,
		main:   main,
		self:   self,
		root:   filepath.Join(main, ".claude", "worktrees"),
		now:    opts.Now,
		cutoff: opts.Now.Add(-opts.MinAge),
		result: Result{Worktrees: []Decision{}},
	}, nil
}

// handle は 1 つの worktree を判定し、回収対象なら回収する。
func (p *pruner) handle(e entry) error {
	path := canon(e.Path)
	if path == p.main || path == p.self || !strings.HasPrefix(path, p.root+string(filepath.Separator)) {
		return nil
	}
	deadPID := 0
	if e.Locked {
		lock := parseLockReason(e.Reason)
		switch lock.Kind {
		case LockAgent:
			// subagent の worktree は本体の sweep が消す。毎セッション報告しないよう黙って飛ばす。
			return nil
		case LockForeign:
			p.record(Decision{Path: path, Action: ActionKept, Reason: ReasonForeignLock, Lock: e.Reason})
			return nil
		case LockSession:
			if p.opts.Alive(lock.PID) {
				return nil
			}
			deadPID = lock.PID
		}
	}
	missing := !dirExists(path)
	var age time.Duration
	if !missing {
		touched := lastTouched(path)
		if touched.After(p.cutoff) {
			return nil
		}
		age = p.now.Sub(touched)
		if !p.opts.Force && isDirty(path) {
			p.record(Decision{Path: path, Action: ActionKept, Reason: ReasonDirty, AgeDays: int(age.Hours() / 24)})
			return nil
		}
	}
	return p.reclaim(path, missing, deadPID != 0, age)
}

// reclaim は判定済みの worktree を消す (DryRun なら表示だけ)。
func (p *pruner) reclaim(path string, missing, locked bool, age time.Duration) error {
	d := Decision{Path: path, Action: ActionRemoved, Reason: ReasonStale, AgeDays: int(age.Hours() / 24)}
	if missing {
		d.Reason, d.AgeDays = ReasonMissing, 0
	}
	if p.opts.DryRun {
		d.Action = ActionWouldRemove
		p.record(d)
		return nil
	}
	if locked {
		if _, err := runGit(p.main, "worktree", "unlock", path); err != nil {
			return err
		}
	}
	if missing {
		// 実体が無いと remove は使えない。unlock 済みなら prune が拾う。prune は他の prunable エントリも一緒に
		// 片付けるが、それらは git 的にどのみち掃除対象なので許容する。
		if _, err := runGit(p.main, "worktree", "prune"); err != nil {
			return err
		}
		p.record(d)
		return nil
	}
	args := []string{"worktree", "remove"}
	if p.opts.Force {
		args = append(args, "--force")
	}
	if _, err := runGit(p.main, append(args, path)...); err != nil {
		return err
	}
	p.record(d)
	return nil
}

// record は判定を Result に積み、同じものを文字で Stdout に書く。
func (p *pruner) record(d Decision) {
	p.result.Worktrees = append(p.result.Worktrees, d)
	writeDecision(p.opts.Stdout, d)
}

// writeDecision は 1 つの判定を人が読む 2 行で w に書く。
func writeDecision(w io.Writer, d Decision) {
	var mark, msg string
	switch {
	case d.Reason == ReasonForeignLock:
		mark, msg = "⏭️ ", "Claude Code 以外の lock なので触りません: "+reasonLabel(d.Lock)
	case d.Reason == ReasonDirty:
		mark, msg = "⚠️ ", "未コミットの変更があるので残します (--force で削除)"
	case d.Action == ActionWouldRemove && d.Reason == ReasonMissing:
		mark, msg = "🔍", "回収対象 (実体が無く登録だけ残っています)"
	case d.Action == ActionWouldRemove:
		mark, msg = "🔍", fmt.Sprintf("回収対象 (最終更新から %d 日)", d.AgeDays)
	case d.Reason == ReasonMissing:
		mark, msg = "🗑️ ", "回収しました (実体が無く登録だけ残っていました)"
	default:
		mark, msg = "🗑️ ", fmt.Sprintf("回収しました (最終更新から %d 日。ブランチは残しています)", d.AgeDays)
	}
	_, _ = fmt.Fprintf(w, "%s %s\n    %s\n", mark, d.Path, msg)
}

func reasonLabel(reason string) string {
	if reason == "" {
		return "(理由なし)"
	}
	return reason
}

// mainWorktree は cwd のリポジトリの main worktree を返す。`git worktree list --porcelain` は main を必ず先頭に出す。
func mainWorktree(cwd string) (string, error) {
	out, err := runGit(cwd, "worktree", "list", "--porcelain")
	if err != nil {
		return "", ErrNotRepository
	}
	first, _, _ := strings.Cut(out, "\n")
	path, ok := strings.CutPrefix(first, "worktree ")
	if !ok {
		return "", ErrNotRepository
	}
	return canon(path), nil
}

// listWorktrees は repo の worktree を porcelain 出力から読む。
func listWorktrees(repo string) ([]entry, error) {
	out, err := runGit(repo, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	var entries []entry
	var cur *entry
	for line := range strings.SplitSeq(out, "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			entries = append(entries, entry{Path: strings.TrimPrefix(line, "worktree ")})
			cur = &entries[len(entries)-1]
		case cur == nil:
			continue
		case line == "locked":
			cur.Locked = true
		case strings.HasPrefix(line, "locked "):
			cur.Locked = true
			cur.Reason = strings.TrimPrefix(line, "locked ")
		}
	}
	return entries, nil
}

// lastTouched は worktree の最終更新。ルートの mtime・HEAD のコミット日時・管理 index の mtime の最大を採る。
// ルートの mtime だけではサブディレクトリ配下の編集を拾えず、HEAD のコミット日時だけでは checkout や index の更新を
// 拾えない。ルートの mtime すら読めないときは「今」とみなす (回収しない側に倒す)。
func lastTouched(wt string) time.Time {
	info, err := os.Stat(wt)
	if err != nil {
		return time.Now()
	}
	newest := info.ModTime()
	if out, err := runGit(wt, "log", "-1", "--format=%ct"); err == nil {
		if sec, err := strconv.ParseInt(out, 10, 64); err == nil {
			newest = later(newest, time.Unix(sec, 0))
		}
	}
	if admin, err := runGit(wt, "rev-parse", "--absolute-git-dir"); err == nil {
		if info, err := os.Stat(filepath.Join(admin, "index")); err == nil {
			newest = later(newest, info.ModTime())
		}
	}
	return newest
}

func later(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

// isDirty は未コミット・未追跡の変更があるかを返す。status が取れないときは dirty 扱い (消さない側に倒す)。
func isDirty(wt string) bool {
	out, err := runGit(wt, "status", "--porcelain")
	return err != nil || out != ""
}

func dirExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

// canon はパスの symlink を解いて比較できる形にする。実体が無いパスは親まで解く。
func canon(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	if dir, err := filepath.EvalSymlinks(filepath.Dir(p)); err == nil {
		return filepath.Join(dir, filepath.Base(p))
	}
	return filepath.Clean(p)
}

// runGit は dir で git を実行し、stdout を trim して返す。失敗は stderr を含めて返す。
func runGit(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...) // #nosec G204 -- 引数は固定の git サブコマンドと worktree list 由来のパス
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}
