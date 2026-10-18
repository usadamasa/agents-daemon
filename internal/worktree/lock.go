package worktree

import (
	"errors"
	"os"
	"regexp"
	"strconv"
	"strings"
	"syscall"
)

// LockKind は worktree の lock reason から読み取った、誰が押さえているか。
type LockKind int

const (
	// LockForeign は Claude Code 以外 (人手、理由なし、書式が読めない) の lock。触らない。
	LockForeign LockKind = iota
	// LockSession は foreground セッション (`--worktree` / EnterWorktree) の lock。PID が死んでいれば回収する。
	LockSession
	// LockAgent は subagent の lock。本体の sweep (cleanupPeriodDays) の担当なので触らない。
	LockAgent
)

// LockReason は lock reason の解釈。Claude Code の lock なら PID が入る。
type LockReason struct {
	Kind LockKind
	PID  int
}

// cc-premise: 2.1.302 worktrees lock reason は `claude <session|agent> <name> (pid N[ start <procStart>])`。start は省かれることがある
var lockPIDPattern = regexp.MustCompile(`\(pid ([0-9]+)`)

// ParseLockReason は `git worktree list --porcelain` の locked 行の reason を読む。
// Claude Code は必ず reason を付けるので、理由なしの lock は人手のものとみなす。
func ParseLockReason(reason string) LockReason {
	var kind LockKind
	switch {
	case strings.HasPrefix(reason, "claude session "):
		kind = LockSession
	case strings.HasPrefix(reason, "claude agent "):
		kind = LockAgent
	default:
		return LockReason{Kind: LockForeign}
	}
	m := lockPIDPattern.FindStringSubmatch(reason)
	if m == nil {
		return LockReason{Kind: LockForeign}
	}
	pid, err := strconv.Atoi(m[1])
	if err != nil {
		return LockReason{Kind: LockForeign}
	}
	return LockReason{Kind: kind, PID: pid}
}

// ProcessAlive は pid のプロセスが生きているかを signal 0 で確かめる。
// ps は sandbox 下の Claude Code からは `Operation not permitted` で起動できず、全 PID を「不在」と誤判定して
// 生きているセッションの worktree まで消してしまうので使わない。
// コマンド名までは見ないため、PID が別プロセスに使い回されていると「生存」と誤判定する。その場合 worktree が
// 残るだけなので安全側に倒れる。
func ProcessAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = proc.Signal(syscall.Signal(0))
	// 別ユーザーのプロセスには EPERM が返るが、存在はしている。
	return err == nil || errors.Is(err, syscall.EPERM)
}
