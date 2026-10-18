package harvest

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRun_PrunesExpiredStateBeforeWriting(t *testing.T) {
	stateDir := t.TempDir()
	harvestDir := filepath.Join(stateDir, "harvest")
	if err := os.MkdirAll(harvestDir, 0o750); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(harvestDir, "2020-01-01.jsonl")
	if err := os.WriteFile(old, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ts := time.Now().Add(-400 * 24 * time.Hour)
	if err := os.Chtimes(old, ts, ts); err != nil {
		t.Fatal(err)
	}

	// projects が空でも Run は成功し、掃除と state の保存だけが起きる。
	err := Run(Opts{ProjectsDir: t.TempDir(), StateDir: stateDir, Days: 14, Home: t.TempDir()})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Errorf("保持期間を過ぎた %s が残っている", old)
	}
	if _, err := os.Stat(filepath.Join(stateDir, stateFileName)); err != nil {
		t.Errorf("harvest-state.json が書かれていない: %v", err)
	}
}

func TestRun_RequiresStateDir(t *testing.T) {
	if err := Run(Opts{ProjectsDir: t.TempDir(), Home: t.TempDir()}); err == nil {
		t.Fatal("StateDir 未指定で Run が成功した; 呼び出し側の解決漏れを拾うべき")
	}
}
