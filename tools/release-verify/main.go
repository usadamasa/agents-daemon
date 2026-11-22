// release-verify は、常用の環境から切り離した環境で、commit から建てたバイナリを実データの複製に当てる保守用の道具。
//
//	release-verify <up|check|open|approve|status|down|run> [ref]
//
// ref は引数、無ければ環境変数 REF、どちらも無ければ HEAD。exit code は、拒否と前提の欠落が 2、点検の fail と
// pass でない status が 1。
package main

import (
	"context"
	"fmt"
	"os"
	"syscall"

	"github.com/usadamasa/agents-daemon/tools/release-verify/releaseverify"
)

func main() {
	// 環境には memory と作業の記録の複製が入るので、ほかの利用者に読ませない。
	syscall.Umask(0o077)
	err := run(context.Background(), os.Args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "release-verify: %v\n", err)
	}
	os.Exit(releaseverify.ExitCode(err))
}

func run(ctx context.Context, args []string) error {
	command, ref := "", os.Getenv("REF")
	if len(args) > 0 {
		command = args[0]
	}
	if len(args) > 1 {
		ref = args[1]
	}
	if ref == "" {
		ref = "HEAD"
	}
	cfg, err := releaseverify.FromEnv(ctx)
	if err != nil {
		return err
	}
	return releaseverify.Run(ctx, cfg, command, ref)
}
