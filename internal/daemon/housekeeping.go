package daemon

import (
	"time"

	"github.com/usadamasa/agents-daemon/internal/applog"
	"github.com/usadamasa/agents-daemon/internal/boardlog"
	"github.com/usadamasa/agents-daemon/internal/daemon/limitlog"
	"github.com/usadamasa/agents-daemon/internal/edge"
	"github.com/usadamasa/agents-daemon/internal/parking"
	"github.com/usadamasa/agents-daemon/internal/tasklog"
)

// ランタイム状態は揮発物として扱い、daemon 自身が古いものを消す。sidecar file の
// 保持期間は書式のパッケージ (ratelimit、sessioncontext、tasklog、parking、edge、limitlog) が持つ (どれだけ古い state が
// 判定に効くかは、その state を読む側の知識のため)。ここに残すのはログの保持期間と、掃除の間隔だけ。
const (
	// logRetention は rotate 済みログ (daemon.log.YYYY-MM-DD と board.log.YYYY-MM-DD) を残す期間。
	// ログは出来事があったときだけ伸びるので、1 週間ぶんあれば振り返りに足りる。
	logRetention = 7 * 24 * time.Hour
	// housekeepInterval は削除を試みる間隔。起動時にも 1 回走る。
	housekeepInterval = time.Hour
)

// attachStateDir は housekeeping が消す対象 (tasks / parking / edges / board log / limit-events) の置き場を
// stateDir から配線する。limit-events は daemon が書く先でもある。
func (r *daemonRuntime) attachStateDir(stateDir string) {
	r.tasks = tasklog.New(stateDir)
	r.parking = parking.New(stateDir)
	r.edges = edge.New(stateDir)
	r.boardLog = boardlog.Path(stateDir)
	r.limits = limitlog.New(stateDir)
}

// housekeepDue は前回の housekeeping から housekeepInterval 以上経ったかを返す。
func (r *daemonRuntime) housekeepDue(now time.Time) bool {
	return r.lastHousekeepAt.IsZero() || !now.Before(r.lastHousekeepAt.Add(housekeepInterval))
}

// housekeep は保持期間を過ぎた rotated ログと sidecar file を消す。何かを
// 消したときと失敗したときだけログに書く (静かな tick と同じ扱い)。
func (r *daemonRuntime) housekeep(logPath string, now time.Time) {
	r.lastHousekeepAt = now

	logs := 0
	// ボードの経路のログ (board.log) も daemon.log と同じ規則で消す。
	for _, path := range []string{logPath, r.boardLog} {
		if path == "" {
			continue
		}
		n, err := applog.PruneRotated(path, now, logRetention)
		if err != nil {
			r.log.Logf("rotated ログの削除でエラー: %v", err)
		}
		logs += n
	}
	states, contexts, err := r.store.Prune(now)
	if err != nil {
		r.log.Logf("sidecar file の削除でエラー: %v", err)
	}
	tasks, err := r.tasks.Prune(now)
	if err != nil {
		r.log.Logf("タスクの記録の削除でエラー: %v", err)
	}
	parked, err := r.parking.Prune(now)
	if err != nil {
		r.log.Logf("パーキングの削除でエラー: %v", err)
	}
	edges, err := r.edges.Prune(now)
	if err != nil {
		r.log.Logf("辺の削除でエラー: %v", err)
	}
	limits, err := r.limits.Prune(now)
	if err != nil {
		r.log.Logf("上限の出来事の記録の削除でエラー: %v", err)
	}
	if logs > 0 || states > 0 || contexts > 0 || tasks > 0 || parked > 0 || edges > 0 || limits > 0 {
		r.log.Logf("保持期間を過ぎたファイルを削除しました: rotated ログ %d 件、利用上限 state %d 件、context state %d 件、タスクの記録 %d 件、パーキング %d 件、辺 %d 件、上限の出来事 %d 件", logs, states, contexts, tasks, parked, edges, limits)
	}
}
