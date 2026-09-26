package daemon

import (
	"time"

	"github.com/usadamasa/agents-daemon/internal/applog"
)

// ランタイム状態は揮発物として扱い､daemon 自身が古いものを消す｡sidecar file の
// 保持期間は sessionstate が持つ (どれだけ古い state が判定に効くかは､その state を
// 読む側の知識のため)｡ここに残すのはログの保持期間と､掃除の間隔だけ｡
const (
	// logRetention は rotate 済みログ (daemon.log.YYYY-MM-DD) を残す期間｡
	// ログは出来事があったときだけ伸びるので､1 週間ぶんあれば振り返りに足りる｡
	logRetention = 7 * 24 * time.Hour
	// housekeepInterval は削除を試みる間隔｡起動時にも 1 回走る｡
	housekeepInterval = time.Hour
)

// housekeepDue は前回の housekeeping から housekeepInterval 以上経ったかを返す｡
func (r *daemonRuntime) housekeepDue(now time.Time) bool {
	return r.lastHousekeepAt.IsZero() || !now.Before(r.lastHousekeepAt.Add(housekeepInterval))
}

// housekeep は保持期間を過ぎた rotated ログと sidecar file を消す｡何かを
// 消したときと失敗したときだけログに書く (静かな tick と同じ扱い)｡
func (r *daemonRuntime) housekeep(logPath string, now time.Time) {
	r.lastHousekeepAt = now

	logs, err := applog.PruneRotated(logPath, now, logRetention)
	if err != nil {
		r.log.Logf("rotated ログの削除でエラー: %v", err)
	}
	states, compacts, err := r.store.Prune(now)
	if err != nil {
		r.log.Logf("sidecar file の削除でエラー: %v", err)
	}
	if logs > 0 || states > 0 || compacts > 0 {
		r.log.Logf("保持期間を過ぎたファイルを削除しました: rotated ログ %d 件､利用上限 state %d 件､compact state %d 件", logs, states, compacts)
	}
}
