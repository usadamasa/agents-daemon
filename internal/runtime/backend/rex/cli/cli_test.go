package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 子の rex には REX_SESSION と REX_BLOCK を渡さない (rex が block の解決をその session の中に閉じる)。ほかの環境変数は残す。
func TestRunner_DropsRexSessionFromChildEnv(t *testing.T) {
	t.Setenv("REX_SESSION", "session:a")
	t.Setenv("REX_BLOCK", "block:a")
	t.Setenv("REX_FAKE_KEEP", "kept")
	dir := t.TempDir()
	bin := filepath.Join(dir, "rex")
	envLog := filepath.Join(dir, "env.log")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nenv > '"+envLog+"'\n"), 0o755); err != nil { // #nosec G306 -- テストの実行ファイル
		t.Fatal(err)
	}
	if _, err := New(bin, 0).Run(context.Background(), "session", "kill", "session:a"); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	b, err := os.ReadFile(envLog) // #nosec G304 -- テストの一時ファイル
	if err != nil {
		t.Fatal(err)
	}
	kept := false
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "REX_SESSION=") || strings.HasPrefix(line, "REX_BLOCK=") {
			t.Errorf("子プロセスに渡った: %q", line)
		}
		if line == "REX_FAKE_KEEP=kept" {
			kept = true
		}
	}
	if !kept {
		t.Error("REX_SESSION / REX_BLOCK 以外の環境変数まで落ちている")
	}
}

func TestRunner_Timeout(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "rex")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nsleep 2\n"), 0o755); err != nil { // #nosec G306 -- テストの実行ファイル
		t.Fatal(err)
	}
	start := time.Now()
	_, err := New(bin, 50*time.Millisecond).Run(context.Background(), "events")
	if err == nil || !strings.Contains(err.Error(), "タイムアウト") {
		t.Fatalf("Run() error = %v, want タイムアウト", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("elapsed = %v, want タイムアウトで打ち切る", elapsed)
	}
}
