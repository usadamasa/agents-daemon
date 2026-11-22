// Package releaseverify は、常用の環境から切り離した環境を作り、commit から建てたバイナリを実データの複製に当てる。
package releaseverify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/usadamasa/agents-daemon/tools/release-verify/releaseverify/check"
	"github.com/usadamasa/agents-daemon/tools/release-verify/releaseverify/command"
	"github.com/usadamasa/agents-daemon/tools/release-verify/releaseverify/record"
)

// Config は、検証の対象と、複製元と、環境の置き場。
type Config struct {
	// Repo は検証する repo の作業ツリー。
	Repo string
	// Base は環境の置き場。環境は Base/<tree の先頭 12 桁> に作る。
	Base string
	// SourceState、SourceClaude、SourceConfig は複製元 (常用の state、Claude の設定ディレクトリ、config.json)。
	SourceState  string
	SourceClaude string
	SourceConfig string
	// BaselineBin は常用のバイナリ。同じ複製に当てた出力を比べる。RealApp は常用のアプリ。
	BaselineBin string
	RealApp     string
	// RexSocket は本物の rex の control socket。daemon だけが繋ぐ。
	RexSocket string
	// Project は dream の project のディレクトリ。
	Project string
	// DaemonRun は daemon を回す時間。
	DaemonRun time.Duration
	// BuildApp は依存図のアプリを建てるか。WriteGuard は daemon の書き込みを sandbox-exec で拒むか。どちらも macOS だけ。
	BuildApp   bool
	WriteGuard bool
	// Keep は run が通った後も環境を残すか。
	Keep bool
	// Stdout は status の JSON、Stderr は経過の出力先。
	Stdout io.Writer
	Stderr io.Writer
}

// RefusalError は、前提が欠けているか、安全の条件に反するので何もしなかったことを表す。exit code は 2。
type RefusalError struct {
	Message string
}

func (e *RefusalError) Error() string {
	return e.Message
}

func refuse(format string, args ...any) error {
	return &RefusalError{Message: fmt.Sprintf(format, args...)}
}

var (
	// ErrCheckFailed は、機械の点検に fail の項目があったことを表す。
	ErrCheckFailed = errors.New("機械の点検が落ちた")
	// ErrNotPassed は、status が pass ではないことを表す。
	ErrNotPassed = errors.New("status は pass ではない (stdout の result を見る)")
)

// ExitCode は Run の error を exit code にする。
func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	if _, ok := errors.AsType[*RefusalError](err); ok {
		return 2
	}
	return 1
}

// Run は command (up / check / open / approve / status / down / run) を ref の commit に対して行う。
func Run(ctx context.Context, cfg Config, command, ref string) error {
	steps := map[string]func(*environment, context.Context) error{
		"up":      (*environment).up,
		"check":   (*environment).check,
		"open":    (*environment).open,
		"approve": (*environment).approve,
		"status":  (*environment).status,
		"down":    (*environment).down,
		"run":     (*environment).run,
	}
	step, ok := steps[command]
	if !ok {
		return refuse("使い方: release-verify <up|check|open|approve|status|down|run>")
	}
	e, err := resolve(ctx, cfg, ref)
	if err != nil {
		return err
	}
	return step(e, ctx)
}

// run は up → check → down を 1 回で行う。check が落ちたら、report を読めるように環境を残す。
func (e *environment) run(ctx context.Context) error {
	if err := e.up(ctx); err != nil {
		return err
	}
	if err := e.check(ctx); err != nil {
		e.info("環境は残してある。report を読んだら down で消す: %s", e.root)
		return err
	}
	if e.cfg.Keep {
		e.info("環境を残した。open で人が確かめ、approve の後に down で消す: %s", e.root)
		return nil
	}
	return e.down(ctx)
}

// down は環境を消す。この道具が作った環境であることを確かめてから消す。
func (e *environment) down(_ context.Context) error {
	if _, err := os.Lstat(e.root); errors.Is(err, os.ErrNotExist) {
		e.info("環境は無い: %s", e.root)
		return nil
	}
	if err := e.require(); err != nil {
		return err
	}
	if err := os.RemoveAll(e.root); err != nil {
		return err
	}
	e.info("環境を消した: %s", e.root)
	return nil
}

// open は隔離した env で依存図のアプリを起こし、窓が閉じるまで待つ。
func (e *environment) open(ctx context.Context) error {
	if err := e.require(); err != nil {
		return err
	}
	bin := e.appBinary()
	if _, err := os.Stat(bin); err != nil {
		return refuse("アプリを建てていない: %s", e.app)
	}
	e.info("隔離した env でアプリを起こす (agent-tasks %s)。ウィンドウを閉じると戻る", e.version())
	// open(1) は env を継がないので、bundle の中の実行ファイルを直に起こす。
	cmd := command.Isolated(ctx, e.cliEnv(), nil, bin)
	cmd.Stdout, cmd.Stderr = e.cfg.Stderr, e.cfg.Stderr
	return cmd.Run()
}

// check は機械の点検を回し、全部通れば記録を書く。
func (e *environment) check(ctx context.Context) error {
	if err := e.require(); err != nil {
		return err
	}
	target := check.Target{
		Root: e.root, Src: filepath.Join(e.root, "src"), Report: e.report, Bin: e.bin, BaselineBin: e.cfg.BaselineBin,
		CLIEnv: e.cliEnv(), DaemonEnv: e.daemonEnv(), Project: e.cfg.Project, RexSocket: e.cfg.RexSocket,
		DaemonLog: filepath.Join(e.state, "logs", "daemon.log"), DaemonRun: e.cfg.DaemonRun,
		Fingerprint: e.fingerprint, FingerprintBefore: filepath.Join(e.root, "fingerprint.before"), Info: e.info,
	}
	if e.cfg.WriteGuard {
		target.WriteGuard = e.cfg.SourceClaude
	}
	rows, failed, err := check.Run(ctx, target)
	if err != nil {
		return err
	}
	if failed {
		e.info("機械の点検が落ちた。記録は書かない。report: %s", e.report)
		return ErrCheckFailed
	}
	checks := make([]record.Check, 0, len(rows))
	for _, r := range rows {
		checks = append(checks, record.Check{Name: r.Name, Result: r.Result})
	}
	if err := e.record().WriteMachinePass(checks); err != nil {
		return refuse("%v", err)
	}
	e.info("機械の点検が通った。記録を tree %s に書いた", e.tree[:12])
	return nil
}

// approve は人の確認を記録に書く。
func (e *environment) approve(_ context.Context) error {
	if err := e.require(); err != nil {
		return err
	}
	if err := e.record().Approve(); err != nil {
		return refuse("%v", err)
	}
	e.info("人の確認を tree %s に書いた", e.tree[:12])
	return nil
}

// status は、ref の tree に通った記録があるかを 1 行の JSON で出す。pass でなければ ErrNotPassed を返す。
func (e *environment) status(ctx context.Context) error {
	status := e.record().Status(ctx)
	enc := json.NewEncoder(e.cfg.Stdout)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(status); err != nil {
		return err
	}
	if status.Result != "pass" {
		return ErrNotPassed
	}
	return nil
}
