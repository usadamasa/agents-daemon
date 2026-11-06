package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDaemonBinFollowsXDGCacheHome(t *testing.T) {
	t.Setenv("HOME", "/home/u")
	t.Setenv("XDG_CACHE_HOME", "")
	if got := daemonBin(); got != "/home/u/.cache/agents-daemon/bin/agents-daemon" {
		t.Fatalf("XDG_CACHE_HOME が無いとき: %s", got)
	}
	t.Setenv("XDG_CACHE_HOME", "/x/cache")
	if got := daemonBin(); got != "/x/cache/agents-daemon/bin/agents-daemon" {
		t.Fatalf("XDG_CACHE_HOME があるとき: %s", got)
	}
}

// fakeBin は stdin をそのまま stdout に写し、FAKE_EXIT が非 0 なら stderr に理由を書いて落ちるスクリプト。
func fakeBin(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "agents-daemon")
	script := "#!/bin/sh\nprintf 'args=%s ' \"$*\"\ncat\nif [ -n \"$FAKE_EXIT\" ]; then echo broken >&2; exit \"$FAKE_EXIT\"; fi\n"
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDepsPassesStdinAndReturnsStdout(t *testing.T) {
	d := &Daemon{bin: fakeBin(t)}
	out, err := d.Deps()
	if err != nil {
		t.Fatal(err)
	}
	if out != "args=deps {}" {
		t.Fatalf("stdout: %q", out)
	}
}

func TestDepsReportsStderrOnFailure(t *testing.T) {
	t.Setenv("FAKE_EXIT", "3")
	d := &Daemon{bin: fakeBin(t)}
	_, err := d.Deps()
	if err == nil {
		t.Fatal("exit 3 なのに err が nil")
	}
	if !strings.Contains(err.Error(), "broken") {
		t.Fatalf("stderr が err に無い: %v", err)
	}
}

func TestDepsReportsMissingBinary(t *testing.T) {
	d := &Daemon{bin: filepath.Join(t.TempDir(), "missing")}
	_, err := d.Deps()
	if err == nil {
		t.Fatal("バイナリが無いのに err が nil")
	}
	var pathErr *os.PathError
	if !errors.As(err, &pathErr) && !strings.Contains(err.Error(), "missing") {
		t.Fatalf("バイナリの置き場が err に無い: %v", err)
	}
}
