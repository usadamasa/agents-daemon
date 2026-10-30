package selfupdate

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// writeInstalled は Claude Code の installed_plugins.json を configDir の下に書く。
func writeInstalled(t *testing.T, configDir string, plugins map[string]string) {
	t.Helper()
	var entries []string
	for id, path := range plugins {
		entries = append(entries, `"`+id+`": [{"scope": "user", "installPath": "`+path+`", "version": "x"}]`)
	}
	body := `{"version": 2, "plugins": {` + strings.Join(entries, ",") + `}}`
	dir := filepath.Join(configDir, "plugins")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "installed_plugins.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// 3 つの段を順に走らせる。build は plugin を更新した後の installPath の build-daemon.sh から走らせる
// (更新前の版の script で建てると、古い版のバイナリと stamp になる)。
func TestUpdate_runsStepsInOrder(t *testing.T) {
	config := t.TempDir()
	writeInstalled(t, config, map[string]string{
		"agents-daemon@usadamasa": "/c/agents-daemon/2026.1025.0",
		"other@usadamasa":         "/c/other/1.0.0",
	})
	var calls [][]string
	run := func(name string, args ...string) error {
		calls = append(calls, append([]string{name}, args...))
		if len(args) >= 2 && args[0] == "plugin" && args[1] == "update" {
			writeInstalled(t, config, map[string]string{"agents-daemon@usadamasa": "/c/agents-daemon/2026.1025.1"})
		}
		return nil
	}
	if err := Update(Options{ConfigDir: config, Run: run}); err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"claude", "plugin", "marketplace", "update", "usadamasa"},
		{"claude", "plugin", "update", "agents-daemon@usadamasa"},
		{"/c/agents-daemon/2026.1025.1/hooks/build-daemon.sh"},
	}
	if !reflect.DeepEqual(calls, want) {
		t.Errorf("calls = %v, want %v", calls, want)
	}
}

// 段が失敗したら、その後の段を走らせずに、どの段で落ちたかを返す。
func TestUpdate_stopsAtFailedStep(t *testing.T) {
	config := t.TempDir()
	writeInstalled(t, config, map[string]string{"agents-daemon@usadamasa": "/c/agents-daemon/2026.1025.0"})
	var calls int
	run := func(name string, args ...string) error {
		calls++
		if len(args) >= 2 && args[1] == "update" {
			return errors.New("exit status 1")
		}
		return nil
	}
	err := Update(Options{ConfigDir: config, Run: run})
	if err == nil || !strings.Contains(err.Error(), "claude plugin update agents-daemon@usadamasa") {
		t.Errorf("err = %v, want plugin update の段の失敗", err)
	}
	if calls != 2 {
		t.Errorf("calls = %d, want 2 (build を走らせない)", calls)
	}
}

// install されていなければ、何も走らせずに理由を返す。installPath が絶対パスでなければ build を走らせない。
func TestUpdate_rejectsMissingOrRelativeInstall(t *testing.T) {
	for name, plugins := range map[string]map[string]string{
		"未 install": {"other@usadamasa": "/c/other/1.0.0"},
		"相対パス":      {"agents-daemon@usadamasa": "cache/agents-daemon/2026.1025.0"},
	} {
		t.Run(name, func(t *testing.T) {
			config := t.TempDir()
			writeInstalled(t, config, plugins)
			var built bool
			run := func(name string, _ ...string) error {
				built = built || strings.HasSuffix(name, "build-daemon.sh")
				return nil
			}
			if err := Update(Options{ConfigDir: config, Run: run}); err == nil {
				t.Error("err = nil, want error")
			}
			if built {
				t.Error("build-daemon.sh を走らせた")
			}
		})
	}
}
