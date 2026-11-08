package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/usadamasa/agents-daemon/internal/applog"
	"github.com/usadamasa/agents-daemon/internal/edge"
	"github.com/usadamasa/agents-daemon/internal/eventlog"
	"github.com/usadamasa/agents-daemon/internal/ledger"
	"github.com/usadamasa/agents-daemon/internal/multiplexer"
	"github.com/usadamasa/agents-daemon/internal/multiplexer/multiplexerfake"
	"github.com/usadamasa/agents-daemon/internal/parking"
	"github.com/usadamasa/agents-daemon/internal/refstate"
	"github.com/usadamasa/agents-daemon/internal/runtime/daemon/limitlog"
	"github.com/usadamasa/agents-daemon/internal/sessionstate"
	"github.com/usadamasa/agents-daemon/internal/tasklog"
	"github.com/usadamasa/agents-daemon/internal/turnfailure"
	"github.com/usadamasa/agents-daemon/internal/worklog"
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

// pane が見えているセッションの作業の記録は、何週間書かれていなくても housekeeping で消えない。見えなくなってから
// Retention が経つと消える。
func TestHousekeep_keepsWorkOfLiveSession(t *testing.T) {
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	client := &multiplexerfake.Client{
		PaneListFunc: func(context.Context) ([]multiplexer.Pane, error) {
			return []multiplexer.Pane{claudePaneWithSession("p1", "s-live", multiplexer.AgentStatusIdle)}, nil
		},
	}
	r, statusPath := newTestRuntime(t, client, now)
	dir := filepath.Dir(statusPath)
	r.attachStateDir(dir)
	logPath := filepath.Join(dir, "logs", "daemon.log")
	liveFile := filepath.Join(r.works.Dir, "s-live.json")
	deadFile := filepath.Join(r.works.Dir, "s-dead.json")
	touchAged(t, liveFile, 8*24*time.Hour, now)
	touchAged(t, deadFile, 8*24*time.Hour, now)

	if _, _, err := r.tick(context.Background(), missingConfigPath(t), now); err != nil {
		t.Fatalf("tick() error = %v", err)
	}
	r.housekeep(logPath, now)

	if _, err := os.Stat(liveFile); err != nil {
		t.Errorf("生きているセッションの作業の記録は残るべき: %v", err)
	}
	if _, err := os.Stat(deadFile); !os.IsNotExist(err) {
		t.Errorf("終了したセッションの作業の記録は消えるべき (err=%v)", err)
	}

	r.housekeep(logPath, now.Add(worklog.Retention-time.Hour))
	if _, err := os.Stat(liveFile); err != nil {
		t.Errorf("見えなくなってから Retention 未満の記録は残るべき: %v", err)
	}
	r.housekeep(logPath, now.Add(worklog.Retention+time.Hour))
	if _, err := os.Stat(liveFile); !os.IsNotExist(err) {
		t.Errorf("見えなくなってから Retention を過ぎた記録は消えるべき (err=%v)", err)
	}
}

// writeAged は content を path に書き、mtime を age だけ前にする。
func writeAged(t *testing.T, path, content string, age time.Duration, now time.Time) {
	t.Helper()
	touchAged(t, path, age, now)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	mtime := now.Add(-age)
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}
}

// 終了したセッションの完了タスクは、作業の記録が残っている間は消さない。消すと依存図がタスクの無いセッションと読み、
// 全部終えたセッションを終了のカードで図に戻す (#483)。生きているセッションと、作業の記録の無いセッションの完了タスクは
// 今までどおり消す。
func TestHousekeep_keepsCompletedTasksOfExitedSession(t *testing.T) {
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	client := &multiplexerfake.Client{
		PaneListFunc: func(context.Context) ([]multiplexer.Pane, error) {
			return []multiplexer.Pane{claudePaneWithSession("p1", "s-live", multiplexer.AgentStatusIdle)}, nil
		},
	}
	r, statusPath := newTestRuntime(t, client, now)
	dir := filepath.Dir(statusPath)
	r.attachStateDir(dir)
	logPath := filepath.Join(dir, "logs", "daemon.log")

	completed := func(session string) string {
		path := filepath.Join(r.tasks.Dir, session, "1.json")
		writeAged(t, path, `{"id":"1","session_id":"`+session+`","status":"completed"}`, 25*time.Hour, now)
		return path
	}
	work := func(session string) string {
		path := filepath.Join(r.works.Dir, session+".json")
		writeAged(t, path, `{"session_id":"`+session+`"}`, time.Hour, now)
		return path
	}
	liveTask := completed("s-live")
	exitedTask := completed("s-exited")
	noWorkTask := completed("s-no-work")
	work("s-live")
	exitedWork := work("s-exited")

	if _, _, err := r.tick(context.Background(), missingConfigPath(t), now); err != nil {
		t.Fatalf("tick() error = %v", err)
	}
	r.housekeep(logPath, now)

	if _, err := os.Stat(exitedTask); err != nil {
		t.Errorf("作業の記録が残っている終了したセッションの完了タスクは残るべき: %v", err)
	}
	if _, err := os.Stat(liveTask); !os.IsNotExist(err) {
		t.Errorf("生きているセッションの完了タスクは消えるべき (err=%v)", err)
	}
	if _, err := os.Stat(noWorkTask); !os.IsNotExist(err) {
		t.Errorf("作業の記録の無いセッションの完了タスクは消えるべき (err=%v)", err)
	}

	// 作業の記録が保持期間で消える回に、残した完了タスクも一緒に消える。次の回まで残すと、その間に resume した
	// セッションの活動の置き場が無くなる (models/card の LivePlace)。
	expiredAt := now.Add(worklog.Retention + time.Hour)
	if err := os.Chtimes(exitedWork, now, now); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}
	r.housekeep(logPath, expiredAt)
	if _, err := os.Stat(exitedWork); !os.IsNotExist(err) {
		t.Errorf("保持期間を過ぎた作業の記録は消えるべき (err=%v)", err)
	}
	if _, err := os.Stat(exitedTask); !os.IsNotExist(err) {
		t.Errorf("作業の記録が消える回に完了タスクも消えるべき (err=%v)", err)
	}
}

// pane が見えているセッションの未完了のタスクと辺は、何週間書かれていなくても housekeeping で消えない。終了したセッションの
// 分は消える。見えなくなってから Retention が経つと、生きていたセッションの分も消える (#483)。
func TestHousekeep_keepsTasksAndEdgesOfLiveSession(t *testing.T) {
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	var panes []multiplexer.Pane
	client := &multiplexerfake.Client{
		PaneListFunc: func(context.Context) ([]multiplexer.Pane, error) { return panes, nil },
	}
	panes = []multiplexer.Pane{claudePaneWithSession("p1", "s-live", multiplexer.AgentStatusIdle)}
	r, statusPath := newTestRuntime(t, client, now)
	dir := filepath.Dir(statusPath)
	r.attachStateDir(dir)
	logPath := filepath.Join(dir, "logs", "daemon.log")
	old := 8 * 24 * time.Hour

	task := func(session string) string {
		path := filepath.Join(r.tasks.Dir, session, "1.json")
		writeAged(t, path, `{"id":"1","session_id":"`+session+`","status":"in_progress"}`, old, now)
		return path
	}
	edgeOf := func(session string) string {
		added, err := r.edges.Add(edge.Endpoint{Ref: "sid:" + session, CWD: "/w"}, edge.Endpoint{Ref: "task:down/1", CWD: "/w"}, "", now)
		if err != nil {
			t.Fatalf("Add: %v", err)
		}
		path := filepath.Join(r.edges.Dir, added.Edge.ID+".json")
		mtime := now.Add(-old)
		if err := os.Chtimes(path, mtime, mtime); err != nil {
			t.Fatalf("Chtimes: %v", err)
		}
		return path
	}
	liveTask, deadTask := task("s-live"), task("s-dead")
	liveEdge, deadEdge := edgeOf("s-live"), edgeOf("s-dead")

	if _, _, err := r.tick(context.Background(), missingConfigPath(t), now); err != nil {
		t.Fatalf("tick() error = %v", err)
	}
	r.housekeep(logPath, now)

	for _, path := range []string{liveTask, liveEdge} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("生きているセッションの記録は残るべき: %v", err)
		}
	}
	for _, path := range []string{deadTask, deadEdge} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("終了したセッションの記録は消えるべき: %s (err=%v)", path, err)
		}
	}

	r.housekeep(logPath, now.Add(tasklog.Retention+time.Hour))
	for _, path := range []string{liveTask, liveEdge} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("見えなくなってから Retention を過ぎた記録は消えるべき: %s (err=%v)", path, err)
		}
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
		// 外部の参照の状態の写しは 1 週間引き直さなければ消える (refstate のテスト)。
		r.refs = refstate.New(dir)
		refFile := filepath.Join(r.refs.Dir, "r-old.json")
		touchAged(t, refFile, 8*24*time.Hour, now)
		// 作業の記録は 1 週間更新されなければ消える (worklog のテスト)。
		r.works = worklog.New(dir)
		workFile := filepath.Join(r.works.Dir, "s-old.json")
		touchAged(t, workFile, 8*24*time.Hour, now)

		r.housekeep(logPath, now)

		if _, err := os.Stat(workFile); !os.IsNotExist(err) {
			t.Errorf("8 日前の作業の記録は消えるべき (err=%v)", err)
		}

		if _, err := os.Stat(refFile); !os.IsNotExist(err) {
			t.Errorf("8 日前の外部の参照の写しは消えるべき (err=%v)", err)
		}

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
		if !strings.Contains(string(data), "rotated ログ 2 件") || !strings.Contains(string(data), "利用上限 state 1 件") || !strings.Contains(string(data), "タスクの記録 1 件") || !strings.Contains(string(data), "パーキング 1 件") || !strings.Contains(string(data), "辺 1 件") || !strings.Contains(string(data), "上限の出来事 1 件") || !strings.Contains(string(data), "セッション台帳 1 件") || !strings.Contains(string(data), "外部の参照 1 件") || !strings.Contains(string(data), "作業の記録 1 件") {
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
