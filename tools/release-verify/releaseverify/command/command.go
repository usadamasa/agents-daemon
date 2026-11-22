// Package command は、検証の道具が外の command を起こす入口。
package command

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Isolated は、bin を env だけを持つ process として起こす command を作る。wrapper は前に置く command (sandbox-exec)。
func Isolated(ctx context.Context, env, wrapper []string, bin string, args ...string) *exec.Cmd {
	argv := append(append(append([]string{}, wrapper...), bin), args...)
	// #nosec G204 -- 起こすのは、この道具が建てたバイナリと常用のバイナリ。引数は固定
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Env = env
	return cmd
}

// Host は、repo の道具 (git、go、cp など) を呼び手の env で起こす command を作る。devShell の PATH と Go の cache を使う。
func Host(ctx context.Context, name string, args ...string) *exec.Cmd {
	// #nosec G204 -- name は固定の道具の名前。引数は設定と ref から決まる
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = os.Environ()
	return cmd
}

// RunHost は Host の command を回し、失敗したら出力を添えて返す。
func RunHost(ctx context.Context, name string, args ...string) error {
	out, err := Host(ctx, name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %w\n%s", name, strings.Join(args, " "), err, out)
	}
	return nil
}

// GitOutput は repo で git を回し、stdout を返す。
func GitOutput(ctx context.Context, repo string, args ...string) (string, error) {
	out, err := Host(ctx, "git", append([]string{"-C", repo}, args...)...).Output()
	if err != nil {
		if exit, ok := errors.AsType[*exec.ExitError](err); ok {
			return "", fmt.Errorf("git %s: %w\n%s", strings.Join(args, " "), err, exit.Stderr)
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
