package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/usadamasa/agents-daemon/internal/applog"
	"github.com/usadamasa/agents-daemon/internal/edge"
	"github.com/usadamasa/agents-daemon/internal/eventlog"
	"github.com/usadamasa/agents-daemon/internal/ledger"
	"github.com/usadamasa/agents-daemon/internal/multiplexer/multiplexerfake"
	"github.com/usadamasa/agents-daemon/internal/parking"
	"github.com/usadamasa/agents-daemon/internal/runtime/daemon/limitlog"
	"github.com/usadamasa/agents-daemon/internal/sessionstate"
	"github.com/usadamasa/agents-daemon/internal/tasklog"
	"github.com/usadamasa/agents-daemon/internal/turnfailure"
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

	t.Run("古い rotated ログと state を消し、消した数をログに残す", func(t *testing.T) {
		dir := t.TempDir()
		logPath := filepath.Join(dir, "logs", "daemon.log")
		store := sessionstate.New(dir)
		contextDir := store.Context.Dir
		touchAged(t, applog.RotatedPath(logPath, "2026-08-01"), 20*24*time.Hour, now)
		touchAged(t, applog.RotatedPath(logPath, "2026-08-27"), 24*time.Hour, now)
		touchAged(t, filepath.Join(store.RateLimits.Dir, "old.json"), 30*time.Hour, now)
		touchAged(t, filepath.Join(store.RateLimits.Dir, "fresh.json"), time.Hour, now)
		// context の保持期間は rate-limits より長い (上限待ちを跨いで block と session の対応を残す)。
		touchAged(t, filepath.Join(contextDir, "old-session.json"), 8*24*time.Hour, now)
		touchAged(t, filepath.Join(contextDir, "fresh-session.json"), 30*time.Hour, now)

		log := applog.NewLogger(logPath, func() time.Time { return now })
		t.Cleanup(func() { _ = log.Close() })
		r := newDaemonRuntime(&multiplexerfake.Client{}, log, filepath.Join(dir, "status.json"), store, func(time.Duration) {}, now)

		// タスクの記録は 1 週間更新されなければ消える (保持期間の細部は tasklog のテスト)。
		r.tasks = tasklog.New(dir)
		taskFile := filepath.Join(r.tasks.Dir, "s1", "1.json")
		touchAged(t, taskFile, 8*24*time.Hour, now)
		// パーキングは 90 日更新されなければ消える (status ごとの期間は parking のテスト)。
		r.parking = parking.New(dir)
		parkFile := filepath.Join(r.parking.Dir, "p1.json")
		touchAged(t, parkFile, 91*24*time.Hour, now)
		// 辺は 1 週間更新されなければ消える (edge のテスト)。
		r.edges = edge.New(dir)
		edgeFile := filepath.Join(r.edges.Dir, "e1.json")
		touchAged(t, edgeFile, 8*24*time.Hour, now)
		// 出来事のログは daemon.log と同じ 7 日で消す。
		r.eventLog = eventlog.Path(dir)
		eventsOld := applog.RotatedPath(r.eventLog, "2026-08-01")
		eventsFresh := applog.RotatedPath(r.eventLog, "2026-08-27")
		touchAged(t, eventsOld, 20*24*time.Hour, now)
		touchAged(t, eventsFresh, 24*time.Hour, now)
		// 上限の出来事の記録は 30 日で消す (daemon/limitlog のテスト)。
		r.limits = limitlog.New(dir)
		limitFile := filepath.Join(r.limits.Dir, "2026-07-01.jsonl")
		touchAged(t, limitFile, 31*24*time.Hour, now)
		// セッション台帳は 1 週間見えなければ消える (ledger のテスト)。
		r.sessions = ledger.New(dir)
		sessionFile := filepath.Join(r.sessions.Dir, "s-old.json")
		touchAged(t, sessionFile, 8*24*time.Hour, now)
		// turn の失敗の記録は 24 時間で消す (turnfailure のテスト)。
		r.turnFailures = turnfailure.New(dir)
		turnFailureFile := filepath.Join(r.turnFailures.Dir, "s-old.json")
		touchAged(t, turnFailureFile, 25*time.Hour, now)

		r.housekeep(logPath, now)

		if _, err := os.Stat(turnFailureFile); !os.IsNotExist(err) {
			t.Errorf("25 時間前の turn の失敗の記録は消えるべき (err=%v)", err)
		}

		if _, err := os.Stat(sessionFile); !os.IsNotExist(err) {
			t.Errorf("8 日前のセッション台帳は消えるべき (err=%v)", err)
		}

		if _, err := os.Stat(limitFile); !os.IsNotExist(err) {
			t.Errorf("31 日前の上限の出来事の記録は消えるべき (err=%v)", err)
		}

		if _, err := os.Stat(eventsOld); !os.IsNotExist(err) {
			t.Errorf("20 日前の出来事のログは消えるべき (err=%v)", err)
		}
		if _, err := os.Stat(eventsFresh); err != nil {
			t.Errorf("1 日前の出来事のログは残るべき: %v", err)
		}

		if _, err := os.Stat(edgeFile); !os.IsNotExist(err) {
			t.Errorf("8 日前の辺は消えるべき (err=%v)", err)
		}

		if _, err := os.Stat(taskFile); !os.IsNotExist(err) {
			t.Errorf("8 日前のタスクの記録は消えるべき (err=%v)", err)
		}
		if _, err := os.Stat(parkFile); !os.IsNotExist(err) {
			t.Errorf("91 日前のパーキングのメモは消えるべき (err=%v)", err)
		}
		if _, err := os.Stat(filepath.Join(contextDir, "old-session.json")); !os.IsNotExist(err) {
			t.Errorf("8 日前の context は消えるべき (err=%v)", err)
		}
		if _, err := os.Stat(filepath.Join(contextDir, "fresh-session.json")); err != nil {
			t.Errorf("30 時間前の context は残るべき: %v", err)
		}

		if _, err := os.Stat(applog.RotatedPath(logPath, "2026-08-01")); !os.IsNotExist(err) {
			t.Errorf("20 日前の rotated ログは消えるべき (err=%v)", err)
		}
		if _, err := os.Stat(applog.RotatedPath(logPath, "2026-08-27")); err != nil {
			t.Errorf("1 日前の rotated ログは残るべき: %v", err)
		}
		if _, err := os.Stat(filepath.Join(store.RateLimits.Dir, "old.json")); !os.IsNotExist(err) {
			t.Errorf("30 時間前の state は消えるべき (err=%v)", err)
		}
		if _, err := os.Stat(filepath.Join(store.RateLimits.Dir, "fresh.json")); err != nil {
			t.Errorf("1 時間前の state は残るべき: %v", err)
		}

		if err := log.Close(); err != nil {
			t.Fatalf("log.Close: %v", err)
		}
		data, err := os.ReadFile(logPath) // #nosec G304 -- テストの一時ディレクトリ配下
		if err != nil {
			t.Fatalf("ログが読めない: %v", err)
		}
		if !strings.Contains(string(data), "rotated ログ 2 件") || !strings.Contains(string(data), "利用上限 state 1 件") || !strings.Contains(string(data), "タスクの記録 1 件") || !strings.Contains(string(data), "パーキング 1 件") || !strings.Contains(string(data), "辺 1 件") || !strings.Contains(string(data), "上限の出来事 1 件") || !strings.Contains(string(data), "セッション台帳 1 件") {
			t.Errorf("削除件数のログが無い: %q", data)
		}
	})

	t.Run("何も消さなかったときはログに書かない", func(t *testing.T) {
		dir := t.TempDir()
		logPath := filepath.Join(dir, "logs", "daemon.log")
		log := applog.NewLogger(logPath, func() time.Time { return now })
		t.Cleanup(func() { _ = log.Close() })
		r := newDaemonRuntime(&multiplexerfake.Client{}, log, filepath.Join(dir, "status.json"), sessionstate.Store{}, func(time.Duration) {}, now)

		r.housekeep(logPath, now)

		if _, err := os.Stat(logPath); !os.IsNotExist(err) {
			t.Errorf("静かな housekeeping はログファイルを作らないはず (err=%v)", err)
		}
	})

	t.Run("housekeepDue は間隔が空くまで false を返す", func(t *testing.T) {
		log := applog.NewLogger(filepath.Join(t.TempDir(), "daemon.log"), func() time.Time { return now })
		t.Cleanup(func() { _ = log.Close() })
		r := newDaemonRuntime(&multiplexerfake.Client{}, log, "", sessionstate.Store{}, func(time.Duration) {}, now)

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
