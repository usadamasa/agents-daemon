package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/usadamasa/agents-daemon/internal/applog"
	"github.com/usadamasa/agents-daemon/internal/herdrcli/herdrclifake"
	"github.com/usadamasa/agents-daemon/internal/sessionstate"
)

func touchAged(t *testing.T, path string, age time.Duration, now time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	mtime := now.Add(-age)
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}
}

func TestHousekeep(t *testing.T) {
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)

	t.Run("古い rotated ログと state を消し､消した数をログに残す", func(t *testing.T) {
		dir := t.TempDir()
		logPath := filepath.Join(dir, "logs", "daemon.log")
		store := sessionstate.New(dir)
		compactedDir := store.Compacted
		touchAged(t, applog.RotatedPath(logPath, "2026-08-01"), 20*24*time.Hour, now)
		touchAged(t, applog.RotatedPath(logPath, "2026-08-27"), 24*time.Hour, now)
		touchAged(t, filepath.Join(store.RateLimits, "old.json"), 30*time.Hour, now)
		touchAged(t, filepath.Join(store.RateLimits, "fresh.json"), time.Hour, now)
		// 圧縮完了 marker は拡張子を持たない｡保持期間も rate-limits より長い｡
		touchAged(t, filepath.Join(compactedDir, "old-session"), 8*24*time.Hour, now)
		touchAged(t, filepath.Join(compactedDir, "fresh-session"), 30*time.Hour, now)

		log := applog.NewLogger(logPath, func() time.Time { return now })
		t.Cleanup(func() { _ = log.Close() })
		r := newDaemonRuntime(&herdrclifake.Client{}, log, filepath.Join(dir, "status.json"), store, false, func(time.Duration) {}, now)

		r.housekeep(logPath, now)

		if _, err := os.Stat(filepath.Join(compactedDir, "old-session")); !os.IsNotExist(err) {
			t.Errorf("8 日前の marker は消えるべき (err=%v)", err)
		}
		if _, err := os.Stat(filepath.Join(compactedDir, "fresh-session")); err != nil {
			t.Errorf("30 時間前の marker は残るべき: %v", err)
		}

		if _, err := os.Stat(applog.RotatedPath(logPath, "2026-08-01")); !os.IsNotExist(err) {
			t.Errorf("20 日前の rotated ログは消えるべき (err=%v)", err)
		}
		if _, err := os.Stat(applog.RotatedPath(logPath, "2026-08-27")); err != nil {
			t.Errorf("1 日前の rotated ログは残るべき: %v", err)
		}
		if _, err := os.Stat(filepath.Join(store.RateLimits, "old.json")); !os.IsNotExist(err) {
			t.Errorf("30 時間前の state は消えるべき (err=%v)", err)
		}
		if _, err := os.Stat(filepath.Join(store.RateLimits, "fresh.json")); err != nil {
			t.Errorf("1 時間前の state は残るべき: %v", err)
		}

		if err := log.Close(); err != nil {
			t.Fatalf("log.Close: %v", err)
		}
		data, err := os.ReadFile(logPath) // #nosec G304 -- テストの一時ディレクトリ配下
		if err != nil {
			t.Fatalf("ログが読めない: %v", err)
		}
		if !strings.Contains(string(data), "rotated ログ 1 件") || !strings.Contains(string(data), "利用上限 state 1 件") {
			t.Errorf("削除件数のログが無い: %q", data)
		}
	})

	t.Run("何も消さなかったときはログに書かない", func(t *testing.T) {
		dir := t.TempDir()
		logPath := filepath.Join(dir, "logs", "daemon.log")
		log := applog.NewLogger(logPath, func() time.Time { return now })
		t.Cleanup(func() { _ = log.Close() })
		r := newDaemonRuntime(&herdrclifake.Client{}, log, filepath.Join(dir, "status.json"), sessionstate.Store{}, false, func(time.Duration) {}, now)

		r.housekeep(logPath, now)

		if _, err := os.Stat(logPath); !os.IsNotExist(err) {
			t.Errorf("静かな housekeeping はログファイルを作らないはず (err=%v)", err)
		}
	})

	t.Run("housekeepDue は間隔が空くまで false を返す", func(t *testing.T) {
		log := applog.NewLogger(filepath.Join(t.TempDir(), "daemon.log"), func() time.Time { return now })
		t.Cleanup(func() { _ = log.Close() })
		r := newDaemonRuntime(&herdrclifake.Client{}, log, "", sessionstate.Store{}, false, func(time.Duration) {}, now)

		if !r.housekeepDue(now) {
			t.Error("初回は true のはず")
		}
		r.lastHousekeepAt = now
		if r.housekeepDue(now.Add(housekeepInterval - time.Second)) {
			t.Error("間隔未満では false のはず")
		}
		if !r.housekeepDue(now.Add(housekeepInterval)) {
			t.Error("間隔を過ぎたら true のはず")
		}
	})
}
