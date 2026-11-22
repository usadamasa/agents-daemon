// Package check は、検証の環境に建てたバイナリへの機械の点検。
package check

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/usadamasa/agents-daemon/tools/release-verify/releaseverify/command"
)

// crashRe は Go の panic の出力の行頭。
var crashRe = regexp.MustCompile(`(?m)^(panic:|goroutine [0-9])`)

// Target は、点検する環境。
type Target struct {
	// Root は環境の root。Src は commit を展開した先、Report は出力を残す先。
	Root   string
	Src    string
	Report string
	// Bin は建てたバイナリ、BaselineBin は常用のバイナリ。
	Bin         string
	BaselineBin string
	// CLIEnv と DaemonEnv は隔離の env。
	CLIEnv    []string
	DaemonEnv []string
	// Project は dream の project のディレクトリ。
	Project string
	// RexSocket は本物の rex の socket。DaemonLog は隔離の daemon が書く log。
	RexSocket string
	DaemonLog string
	// DaemonRun は daemon を回す時間。
	DaemonRun time.Duration
	// WriteGuard が空でなければ、その下への daemon の書き込みを sandbox-exec で拒む。
	WriteGuard string
	// Fingerprint は常用の環境の要約を出す。FingerprintBefore は up が残した要約のファイル。
	Fingerprint       func() string
	FingerprintBefore string
	// Info は経過を出す。
	Info func(format string, args ...any)
}

// Row は 1 項目の結果。Result は pass / fail / info。
type Row struct {
	Name   string
	Result string
	Detail string
}

type checker struct {
	t    Target
	rows []Row
}

// Run は全項目を回し、結果を Report の checks.tsv にも残す。failed は fail の項目があったこと。
func Run(ctx context.Context, t Target) (rows []Row, failed bool, err error) {
	if err := os.MkdirAll(t.Report, 0o700); err != nil {
		return nil, false, err
	}
	c := &checker{t: t}
	_, statErr := os.Stat(t.Bin)
	c.verdict("binary", statErr == nil, t.Bin)
	c.checkCLI(ctx)
	c.checkDaemon(ctx)
	c.checkUntouched()

	var tsv strings.Builder
	for _, r := range c.rows {
		fmt.Fprintf(&tsv, "%s\t%s\t%s\n", r.Name, r.Result, r.Detail)
		failed = failed || r.Result == "fail"
	}
	if err := os.WriteFile(c.report("checks.tsv"), []byte(tsv.String()), 0o600); err != nil {
		return nil, false, err
	}
	return c.rows, failed, nil
}

func (c *checker) record(name, result, detail string) {
	c.rows = append(c.rows, Row{Name: name, Result: result, Detail: detail})
	if detail != "" {
		detail = ": " + detail
	}
	c.t.Info("[%s] %s%s", result, name, detail)
}

func (c *checker) verdict(name string, ok bool, detail string) {
	result := "pass"
	if !ok {
		result = "fail"
	}
	c.record(name, result, detail)
}

func (c *checker) report(name string) string {
	return filepath.Join(c.t.Report, name)
}

func (c *checker) writeReport(name string, data []byte) {
	// #nosec G703 -- 書く先は環境の下の report。名前は固定
	if err := os.WriteFile(c.report(name), data, 0o600); err != nil {
		c.t.Info("report を書けない: %v", err)
	}
}

func (c *checker) readReport(name string) []byte {
	// #nosec G304 -- 読むのは、この道具が環境の下に書いた report
	data, _ := os.ReadFile(c.report(name))
	return data
}

// runCLI は隔離の env で CLI を 1 つ回し、stdout と stderr を report に残す。起こせなかったときの終了コードは -1。
func (c *checker) runCLI(ctx context.Context, bin, name, stdin string, args ...string) int {
	var stdout, stderr bytes.Buffer
	cmd := command.Isolated(ctx, c.t.CLIEnv, nil, bin, args...)
	cmd.Stdin = strings.NewReader(stdin)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if err != nil && cmd.ProcessState == nil {
		fmt.Fprintf(&stderr, "%v\n", err)
	}
	c.writeReport(name+".out", stdout.Bytes())
	c.writeReport(name+".err", stderr.Bytes())
	if cmd.ProcessState == nil {
		return -1
	}
	return cmd.ProcessState.ExitCode()
}

// crashed は、panic と、利用者向けのエラー (exit 1) を超える終了コードを拾う。指摘を exit 1 で返すコマンドは拾わない。
func crashed(code int, stderr []byte) bool {
	return code < 0 || code > 1 || crashRe.Match(stderr)
}

func (c *checker) checkCLI(ctx context.Context) {
	t := c.t
	code := c.runCLI(ctx, t.Bin, "deps", "{}", "deps")
	c.verdict("deps", code == 0 && json.Valid(c.readReport("deps.out")), fmt.Sprintf("exit %d。%s", code, c.report("deps.err")))

	for name, args := range map[string][]string{
		"dream-policy": {"dream", "policy"},
		"dream-check":  {"dream", "check", "--json", "--project", t.Project},
	} {
		code := c.runCLI(ctx, t.Bin, name, "", args...)
		c.verdict(name, !crashed(code, c.readReport(name+".err")), fmt.Sprintf("exit %d。%s", code, c.report(name+".err")))
	}

	// リリースする tree の skill は、利用者の skill の置き場に無いので上の点検に入らない。置き場を名指しして掛け、
	// 指摘があれば落とす (skill の誤りか、点検の誤検知のどちらかで、どちらもリリースの前に直す)。
	emptyMemory := filepath.Join(t.Root, "tmp", "empty-memory")
	if err := os.MkdirAll(emptyMemory, 0o700); err != nil {
		c.record("dream-check-skills", "fail", err.Error())
		return
	}
	for name, dir := range map[string]string{
		"dream-check-skills-plugin": filepath.Join(t.Src, "skills"),
		"dream-check-skills-repo":   filepath.Join(t.Src, ".claude", "skills"),
	} {
		if _, err := os.Stat(dir); err != nil {
			continue
		}
		code := c.runCLI(ctx, t.Bin, name, "", "dream", "check", "--json", "--memory-dir", emptyMemory, "--skills-dir", dir)
		c.verdict(name, code == 0, fmt.Sprintf("exit %d。%s", code, c.report(name+".out")))
	}
	c.compareBaseline(ctx)
}

// compareBaseline は、常用のバイナリを同じ複製に当てた出力との差分を report に残す。挙動が変わった箇所を人が読む材料で、
// 合否には入れない。
func (c *checker) compareBaseline(ctx context.Context) {
	t := c.t
	if info, err := os.Stat(t.BaselineBin); err != nil || info.Mode()&0o111 == 0 {
		c.record("baseline-diff", "info", "常用のバイナリが無いので比べない: "+t.BaselineBin)
		return
	}
	c.runCLI(ctx, t.BaselineBin, "baseline-deps", "{}", "deps")
	c.runCLI(ctx, t.BaselineBin, "baseline-dream-check", "", "dream", "check", "--json", "--project", t.Project)
	lines := map[string]int{}
	for _, name := range []string{"deps", "dream-check"} {
		// diff は差分があると exit 1 で返す。差分の有無は出力で見る。
		out, _ := command.Host(ctx, "diff", "-u", c.report("baseline-"+name+".out"), c.report(name+".out")).Output()
		c.writeReport(name+".diff", out)
		lines[name] = bytes.Count(out, []byte("\n"))
	}
	c.record("baseline-diff", "info", fmt.Sprintf("deps %d 行、dream check %d 行。%s", lines["deps"], lines["dream-check"],
		c.report("*.diff")))
}

// denyWriteProfile は、dir の下への書き込みを拒む sandbox-exec の profile を出す。
func denyWriteProfile(dir string) string {
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	return fmt.Sprintf(`(version 1)(allow default)(deny file-write* (subpath %q))`, dir)
}

// probeWriteGuard は、同じ形の profile が書き込みを拒むことを、環境の下のディレクトリで確かめる。profile の外へ書けること
// (sandbox-exec 自体は動く) と、中へ書けないことの両方を見る。sandbox の入れ子のように拒めない環境では false を返す。
func (c *checker) probeWriteGuard(ctx context.Context) bool {
	probe := filepath.Join(c.t.Root, "tmp", "guard-probe")
	if err := os.MkdirAll(probe, 0o700); err != nil {
		return false
	}
	profile := denyWriteProfile(probe)
	inside := filepath.Join(probe, "x")
	allowed := command.RunHost(ctx, "sandbox-exec", "-p", profile, "/usr/bin/touch", filepath.Join(c.t.Root, "tmp", "guard-ok"))
	denied := command.RunHost(ctx, "sandbox-exec", "-p", profile, "/usr/bin/touch", inside)
	_, statErr := os.Stat(inside)
	c.writeReport("write-guard.err", fmt.Appendf(nil, "profile の外への書き込み: %v\nprofile の中への書き込み: %v\n", allowed, denied))
	return allowed == nil && denied != nil && statErr != nil
}

// checkDaemon は daemon を起こし、数 tick の後に止める。
func (c *checker) checkDaemon(ctx context.Context) {
	t := c.t
	if _, err := os.Stat(t.RexSocket); err != nil {
		c.record("daemon", "fail", "rex の socket が無い: "+t.RexSocket)
		return
	}
	var wrapper []string
	if t.WriteGuard == "" {
		c.record("write-guard", "info", "書き込みの拒否は無効")
	} else {
		if !c.probeWriteGuard(ctx) {
			c.record("write-guard", "fail", "sandbox-exec が書き込みを拒まない。"+c.report("write-guard.err"))
			c.record("daemon", "fail", "書き込みを拒めないので起こさない (task 経由で、Claude Code の sandbox の外から打つ)")
			return
		}
		c.record("write-guard", "pass", "sandbox-exec が書き込みを拒む")
		wrapper = []string{"sandbox-exec", "-p", denyWriteProfile(t.WriteGuard)}
	}

	early, code, err := c.runDaemon(ctx, wrapper)
	if err != nil {
		c.record("daemon", "fail", err.Error())
		return
	}
	// #nosec G304 -- 読むのは、隔離の daemon が環境の下に書いた log
	log, _ := os.ReadFile(t.DaemonLog)
	if len(log) > 0 {
		c.writeReport("daemon.log", log)
	}
	ok, detail := daemonVerdict(early, code, c.readReport("daemon.out"), log)
	c.verdict("daemon", ok, fmt.Sprintf("%s (%s 回した)。%s", detail, t.DaemonRun, c.report("daemon.out")))
}

// runDaemon は daemon を --dry-run で起こし、DaemonRun の後に SIGTERM で止める。early は、止める前に自分で終わったこと。
func (c *checker) runDaemon(ctx context.Context, wrapper []string) (early bool, code int, err error) {
	// #nosec G304 -- 書くのは環境の下の report
	out, err := os.OpenFile(c.report("daemon.out"), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return false, 0, err
	}
	defer func() { _ = out.Close() }()
	cmd := command.Isolated(ctx, c.t.DaemonEnv, wrapper, c.t.Bin, "daemon", "--foreground", "--dry-run")
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Start(); err != nil {
		return false, 0, err
	}
	done := make(chan struct{})
	go func() {
		// 終了の状態は ProcessState から読む。
		_ = cmd.Wait()
		close(done)
	}()
	select {
	case <-done:
		early = true
	case <-time.After(c.t.DaemonRun):
		if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
			c.t.Info("daemon へ SIGTERM を送れない: %v", err)
		}
		<-done
	}
	return early, cmd.ProcessState.ExitCode(), nil
}

// daemonVerdict は daemon の終わり方から合否を決める。code は、signal で死んだときに -1 になる。
func daemonVerdict(early bool, code int, out, log []byte) (ok bool, detail string) {
	switch {
	case early:
		return false, fmt.Sprintf("止める前に終わった (exit %d)", code)
	case code != 0:
		return false, fmt.Sprintf("exit %d", code)
	case crashRe.Match(out):
		return false, "panic"
	case bytes.Contains(bytes.ToLower(log), []byte("operation not permitted")):
		return false, "書き込みを拒まれた (Claude の設定ディレクトリへ書こうとした疑い)"
	}
	return true, "SIGTERM で終わった"
}

// checkUntouched は、常用のバイナリとアプリと設定が up の後に変わっていないことを見る。
func (c *checker) checkUntouched() {
	// #nosec G304 -- 読むのは、up が環境の下に書いた要約
	before, err := os.ReadFile(c.t.FingerprintBefore)
	after := c.t.Fingerprint()
	c.writeReport("fingerprint.after", []byte(after))
	if err == nil && string(before) == after {
		c.record("untouched", "pass", "常用のバイナリとアプリと設定は変わっていない")
		return
	}
	c.record("untouched", "fail", "常用の環境が up の後に変わった。"+c.report("fingerprint.after")+" と "+c.t.FingerprintBefore)
}
