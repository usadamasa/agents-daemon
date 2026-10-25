package parkcli

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/usadamasa/agents-daemon/internal/eventlog"
	"github.com/usadamasa/agents-daemon/internal/parking"
	"github.com/usadamasa/agents-daemon/internal/tasklog"
)

// park の出来事 (internal/eventlog の package doc の一覧) を、park の操作の結果から組んで書く。
// eventlog に持たせないのは、eventlog が tasklog と parking を import しないため (tasklog の Ca を増やさない)。

// parkAdd は stdin の Request で s.Park を走らせ、結果をログに書く (park add)。
func parkAdd(stateDir string, s parking.Store, stdin io.Reader, cwd string, tasks tasklog.Store, now time.Time) (parking.Added, error) {
	req, err := parking.ReadRequest(stdin)
	if err != nil {
		parkFailed(stateDir, now, "add", "", err)
		return parking.Added{}, err
	}
	added, err := s.Park(req, cwd, tasks, now)
	if err != nil {
		parkFailed(stateDir, now, "add", req.SessionID, err)
		return added, err
	}
	fields := []string{kv("memo", added.Item.ID), kv("kind", added.Item.Kind), kv("created", added.Created)}
	// 同じ置き方をまとめたときは sources に足さないので、最後の source では今回のタスクを引けない。
	if task := tasks.ActiveTaskID(req.SessionID); task != "" {
		fields = append(fields, kv("task", task))
	}
	_ = eventlog.Record(stateDir, req.SessionID, "park.add", fields, now)
	return added, nil
}

// parkSet は s.Set を走らせ、status の変化を書く (park set)。session は操作したセッションで、ログに添える。
func parkSet(stateDir string, s parking.Store, session, id, status, expect string, now time.Time) (parking.Item, error) {
	item, from, err := s.Set(id, status, expect)
	if err != nil {
		parkFailed(stateDir, now, "set", session, err, kv("memo", id))
		return item, err
	}
	_ = eventlog.Record(stateDir, session, "park.set", []string{kv("memo", id), kv("from", from), kv("to", status)}, now)
	return item, nil
}

// parkDiscard は s.DiscardBefore を走らせ、捨てたメモの id を書く (park discard)。id を書くのは、まとめて捨てたものを
// `park set <id> --status open` で戻せるようにするため。
func parkDiscard(stateDir string, s parking.Store, session string, days int, now time.Time) ([]parking.Item, error) {
	if days < 1 {
		err := fmt.Errorf("--days は 1 以上 (%d)", days)
		parkFailed(stateDir, now, "discard", session, err, kv("days", days))
		return nil, err
	}
	items, err := s.DiscardBefore(now.Add(-time.Duration(days) * 24 * time.Hour))
	if err != nil {
		parkFailed(stateDir, now, "discard", session, err, kv("days", days))
		return nil, err
	}
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	_ = eventlog.Record(stateDir, session, "park.discard", []string{kv("days", days), kv("memos", len(items)), kv("ids", strings.Join(ids, ","))}, now)
	return items, nil
}

// parkAdopt は s.Adopt を走らせ、移したタスクとメモを書く (park adopt)。exited はセッションが終了したと確かめる
// (verify.SessionExited)。viewer は操作したセッションで、ログに添える。
func parkAdopt(stateDir string, s parking.Store, tasks tasklog.Store, viewer, sessionID, taskID string, exited func(string, time.Time) error, now time.Time) (parking.Added, error) {
	added, err := s.Adopt(sessionID, taskID, tasks, exited, now)
	if err != nil {
		parkFailed(stateDir, now, "adopt", viewer, err, kv("from_session", sessionID), kv("task", taskID))
		return added, err
	}
	fields := []string{kv("from_session", sessionID), kv("task", taskID), kv("memo", added.Item.ID), kv("created", added.Created)}
	_ = eventlog.Record(stateDir, viewer, "park.adopt", fields, now)
	return added, nil
}

// parkFailed は park の操作の失敗を書く。op を先頭に、session、extra、reason の順に並べる。
func parkFailed(stateDir string, now time.Time, op, session string, err error, extra ...string) {
	fields := []string{kv("op", op)}
	if session != "" {
		fields = append(fields, kv("session", session))
	}
	fields = append(append(fields, extra...), kv("reason", err))
	_ = eventlog.Record(stateDir, "", "park.failed", fields, now)
}

func kv(key string, value any) string {
	return key + "=" + fmt.Sprint(value)
}
