package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
)

func TestStateDirFollowsXDGStateHome(t *testing.T) {
	t.Setenv("HOME", "/home/u")
	t.Setenv("XDG_STATE_HOME", "")
	if got := restorePath(); got != "/home/u/.local/state/agents-daemon/gui/restore.json" {
		t.Fatalf("XDG_STATE_HOME が無いとき: %s", got)
	}
	t.Setenv("XDG_STATE_HOME", "/x/state")
	if got := restorePath(); got != "/x/state/agents-daemon/gui/restore.json" {
		t.Fatalf("XDG_STATE_HOME があるとき: %s", got)
	}
}

func TestNewer(t *testing.T) {
	cases := []struct {
		own, stamp string
		want       bool
	}{
		{"2026.1106.0", "2026.1107.0", true},
		{"2026.0926.0", "2026.1010.0", true}, // 0 始まりの欄も 10 進で比べる
		{"2026.1107.9", "2026.1107.10", true},
		{"2026.1107.0", "2026.1107.0", false},
		{"2026.1107.0", "2026.1106.0", false},
		{"dev", "2026.1107.0", false}, // task gui:build の dev は切り替えない
		{"2026.1107.0", "", false},
		{"2026.1107.0", "2026..0", false},
		{"2026.1107", "2026.1107.1", true}, // 欄の数が違えば足りない欄を 0 とする
	}
	for _, c := range cases {
		if got := newer(c.own, c.stamp); got != c.want {
			t.Errorf("newer(%q, %q) = %v, want %v", c.own, c.stamp, got, c.want)
		}
	}
}

func TestAvailableReturnsNewerStampVersion(t *testing.T) {
	stamp := filepath.Join(t.TempDir(), "agent-tasks.version")
	u := &upgrader{version: "2026.1107.0", stamp: stamp}
	if v, ok := u.available(); ok || v != "" {
		t.Fatalf("stamp が無いのに (%q, %v)", v, ok)
	}
	if err := os.WriteFile(stamp, []byte("2026.1107.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := u.available(); ok {
		t.Fatal("同じ version で ok")
	}
	if err := os.WriteFile(stamp, []byte("2026.1107.99\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if v, ok := u.available(); !ok || v != "2026.1107.99" {
		t.Fatalf("新しい stamp で (%q, %v)", v, ok)
	}
}

func TestReadStampTrimsNewline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent-tasks.version")
	if err := os.WriteFile(path, []byte("2026.1107.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := readStamp(path); got != "2026.1107.0" {
		t.Fatalf("readStamp: %q", got)
	}
	if got := readStamp(filepath.Join(t.TempDir(), "missing")); got != "" {
		t.Fatalf("無い stamp: %q", got)
	}
}

func sampleRestore(version string, at time.Time) restoreState {
	return restoreState{
		Version: version,
		SavedAt: at,
		Bounds:  application.Rect{X: 10, Y: 20, Width: 900, Height: 700},
		View:    map[string]json.RawMessage{"selected": json.RawMessage(`{"type":"node","key":"s/1"}`)},
	}
}

func TestRestoreRoundTripRemovesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gui", "restore.json")
	now := time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)
	want := sampleRestore("2026.1107.0", now)
	if err := saveRestore(path, want); err != nil {
		t.Fatal(err)
	}
	got, ok := loadRestore(path, "2026.1107.0", now.Add(30*time.Second))
	if !ok {
		t.Fatal("保存した直後の restore を読まなかった")
	}
	if got.Bounds != want.Bounds || string(got.View["selected"]) != string(want.View["selected"]) {
		t.Fatalf("読んだ値が違う: %+v", got)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("読んだ後もファイルが残る: %v", err)
	}
}

func TestRestoreDiscardsStaleOrMismatched(t *testing.T) {
	now := time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)
	cases := map[string]struct {
		saved   restoreState
		version string
	}{
		"古い":          {sampleRestore("2026.1107.0", now.Add(-restoreMaxAge-time.Second)), "2026.1107.0"},
		"version が違う": {sampleRestore("2026.1107.0", now), "2026.1106.0"},
		"未来の時刻":       {sampleRestore("2026.1107.0", now.Add(restoreMaxAge+time.Second)), "2026.1107.0"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "restore.json")
			if err := saveRestore(path, c.saved); err != nil {
				t.Fatal(err)
			}
			if _, ok := loadRestore(path, c.version, now); ok {
				t.Fatal("捨てるべき restore を読んだ")
			}
			if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("捨てた restore が残る: %v", err)
			}
		})
	}
}

func TestRestoreDiscardsBrokenFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "restore.json")
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := loadRestore(path, "2026.1107.0", time.Now()); ok {
		t.Fatal("壊れたファイルを読んだ")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("壊れたファイルが残る: %v", err)
	}
}

func TestRestoreMissingFile(t *testing.T) {
	if _, ok := loadRestore(filepath.Join(t.TempDir(), "missing"), "2026.1107.0", time.Now()); ok {
		t.Fatal("無いファイルで ok")
	}
}

func TestUpgradeSavesRestoreThenLaunchesInBackground(t *testing.T) {
	dir := t.TempDir()
	stamp := filepath.Join(dir, "agent-tasks.version")
	if err := os.WriteFile(stamp, []byte("2026.1107.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	restore := filepath.Join(dir, "gui", "restore.json")
	var launched []string
	u := &upgrader{
		version: "2026.1106.0",
		stamp:   stamp,
		restore: restore,
		app:     "/Apps/agent-tasks.app",
		launch: func(name string, args ...string) error {
			// 起こす時点で restore が書けていること (新しい側がすぐ読む)
			if _, err := os.Stat(restore); err != nil {
				t.Fatalf("起こす前に restore が無い: %v", err)
			}
			launched = append([]string{name}, args...)
			return nil
		},
		now: func() time.Time { return time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC) },
	}
	bounds := application.Rect{X: 1, Y: 2, Width: 3, Height: 4}
	if err := u.switchTo(bounds, `{"showCompleted":true}`); err != nil {
		t.Fatal(err)
	}
	want := []string{"open", "-g", "-n", "/Apps/agent-tasks.app"}
	if len(launched) != len(want) {
		t.Fatalf("起こしたコマンド: %v", launched)
	}
	for i := range want {
		if launched[i] != want[i] {
			t.Fatalf("起こしたコマンド: %v", launched)
		}
	}
	got, ok := loadRestore(restore, "2026.1107.0", u.now())
	if !ok {
		t.Fatal("新しい version で restore が読めない")
	}
	if got.Bounds != bounds || string(got.View["showCompleted"]) != "true" {
		t.Fatalf("restore の中身: %+v", got)
	}
}

func TestUpgradeRefusesWhenStampIsNotNewer(t *testing.T) {
	dir := t.TempDir()
	stamp := filepath.Join(dir, "agent-tasks.version")
	if err := os.WriteFile(stamp, []byte("2026.1106.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	u := &upgrader{
		version: "2026.1106.0",
		stamp:   stamp,
		restore: filepath.Join(dir, "restore.json"),
		app:     "/Apps/agent-tasks.app",
		launch: func(string, ...string) error {
			t.Fatal("新しくないのに起こした")
			return nil
		},
		now: time.Now,
	}
	if err := u.switchTo(application.Rect{}, `{}`); err == nil {
		t.Fatal("新しくないのに err が nil")
	}
}

func TestUpgradeRemovesRestoreWhenLaunchFails(t *testing.T) {
	dir := t.TempDir()
	stamp := filepath.Join(dir, "agent-tasks.version")
	if err := os.WriteFile(stamp, []byte("2026.1107.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	restore := filepath.Join(dir, "restore.json")
	u := &upgrader{
		version: "2026.1106.0",
		stamp:   stamp,
		restore: restore,
		app:     "/Apps/agent-tasks.app",
		launch:  func(string, ...string) error { return errors.New("boom") },
		now:     time.Now,
	}
	if err := u.switchTo(application.Rect{}, `{}`); err == nil {
		t.Fatal("起こせなかったのに err が nil")
	}
	if _, err := os.Stat(restore); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("起こせなかった restore が残る: %v", err)
	}
}

func TestUpgradeRejectsNonObjectView(t *testing.T) {
	dir := t.TempDir()
	stamp := filepath.Join(dir, "agent-tasks.version")
	if err := os.WriteFile(stamp, []byte("2026.1107.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	u := &upgrader{
		version: "2026.1106.0",
		stamp:   stamp,
		restore: filepath.Join(dir, "restore.json"),
		app:     "/Apps/agent-tasks.app",
		launch: func(string, ...string) error {
			t.Fatal("view が読めないのに起こした")
			return nil
		},
		now: time.Now,
	}
	if err := u.switchTo(application.Rect{}, `[1]`); err == nil {
		t.Fatal("配列の view で err が nil")
	}
}
