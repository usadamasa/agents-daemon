package daemon

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/usadamasa/agents-daemon/internal/applog"
	"github.com/usadamasa/agents-daemon/internal/edge"
	"github.com/usadamasa/agents-daemon/internal/eventlog"
	"github.com/usadamasa/agents-daemon/internal/ledger"
	"github.com/usadamasa/agents-daemon/internal/parking"
	"github.com/usadamasa/agents-daemon/internal/refstate"
	"github.com/usadamasa/agents-daemon/internal/runtime/daemon/limitlog"
	"github.com/usadamasa/agents-daemon/internal/tasklog"
	"github.com/usadamasa/agents-daemon/internal/turnfailure"
	"github.com/usadamasa/agents-daemon/internal/worklog"
)

// ランタイム状態は揮発物として扱い、daemon 自身が古いものを消す。sidecar file の
// 保持期間は書式のパッケージ (ratelimit、sessioncontext、tasklog、parking、edge、ledger、limitlog、turnfailure) が持つ (どれだけ古い state が
// 判定に効くかは、その state を読む側の知識のため)。ここに残すのはログの保持期間と、掃除の間隔だけ。
const (
	// logRetention は rotate 済みログ (daemon.log.YYYY-MM-DD と events.log.YYYY-MM-DD) を残す期間。
	// ログは出来事があったときだけ伸びるので、1 週間ぶんあれば振り返りに足りる。
	logRetention = 7 * 24 * time.Hour
	// housekeepInterval は削除を試みる間隔。起動時にも 1 回走る。
	housekeepInterval = time.Hour
)

// attachStateDir は housekeeping が消す対象 (tasks / work / parking / edges / sessions / events log / limit-events /
// turn-failures) の置き場を stateDir から配線する。sessions と limit-events は daemon が書く先、edges は daemon が
// 端点を結びつける先、turn-failures は上限の出来事に添えて読む先でもある。
func (r *daemonRuntime) attachStateDir(stateDir string) {
	r.tasks = tasklog.New(stateDir)
	r.works = worklog.New(stateDir)
	r.parking = parking.New(stateDir)
	r.edges = edge.New(stateDir)
	r.sessions = ledger.New(stateDir)
	r.eventLog = eventlog.Path(stateDir)
	r.limits = limitlog.New(stateDir)
	r.turnFailures = turnfailure.New(stateDir)
	r.refs = refstate.New(stateDir)
}

// housekeepDue は前回の housekeeping から housekeepInterval 以上経ったかを返す。
func (r *daemonRuntime) housekeepDue(now time.Time) bool {
	return r.lastHousekeepAt.IsZero() || !now.Before(r.lastHousekeepAt.Add(housekeepInterval))
}

// sidecar は housekeeping が消す記録の 1 種類。label はログの文面、prune は消した数を返す。
type sidecar struct {
	label string
	prune func(now time.Time) (int, error)
}

// sidecars は保持期間を書式のパッケージが持つ記録の一覧。並びがそのままログの並びになる。
// 記録を足すときはここに 1 行足す (housekeep は変えない)。タスクの記録は作業の記録と辺の後に消す。作業の記録が消える回に
// 残していた完了タスクも消し、作業の記録の無いセッションに完了タスクだけが残る窓を作らない (models/card)。
func (r *daemonRuntime) sidecars() []sidecar {
	return []sidecar{
		{"作業の記録", r.works.Prune},
		{"パーキング", r.parking.Prune},
		{"辺", r.edges.Prune},
		{"タスクの記録", func(now time.Time) (int, error) { return r.tasks.Prune(now, r.keepCompletedTasks) }},
		{"上限の出来事", r.limits.Prune},
		{"セッション台帳", r.sessions.Prune},
		{"turn の失敗", r.turnFailures.Prune},
		{"外部の参照", r.refs.Prune},
	}
}

// keepCompletedTasks はセッションの完了タスクを残すか。終了したセッションの分は、作業の記録が消えるまで残す。起動直後の掃除は
// tick の前で liveSessions が空なので、生きているセッションの分も残り、次の掃除で消える。
func (r *daemonRuntime) keepCompletedTasks(sessionID string) bool {
	_, found := r.works.Get(sessionID)
	return found && !slices.Contains(r.liveSessions, sessionID)
}

// housekeep は保持期間を過ぎた rotated ログと sidecar file を消す。何かを
// 消したときと失敗したときだけログに書く (静かな tick と同じ扱い)。
func (r *daemonRuntime) housekeep(logPath string, now time.Time) {
	r.lastHousekeepAt = now

	logs := 0
	// 出来事のログ (events.log) も daemon.log と同じ規則で消す。
	for _, path := range []string{logPath, r.eventLog} {
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
	removed := logs + states + contexts
	counts := []string{
		fmt.Sprintf("rotated ログ %d 件", logs),
		fmt.Sprintf("利用上限 state %d 件", states),
		fmt.Sprintf("context state %d 件", contexts),
	}
	for _, s := range r.sidecars() {
		n, err := s.prune(now)
		if err != nil {
			r.log.Logf("%sの削除でエラー: %v", s.label, err)
		}
		removed += n
		counts = append(counts, fmt.Sprintf("%s %d 件", s.label, n))
	}
	if removed > 0 {
		r.log.Logf("保持期間を過ぎたファイルを削除しました: %s", strings.Join(counts, "、"))
	}
}
