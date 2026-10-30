package parking

import (
	"fmt"
	"time"

	"github.com/usadamasa/agents-daemon/internal/gitrepo"
	"github.com/usadamasa/agents-daemon/internal/tasklog"
)

// AdoptedDefault は Adopt で移したメモの default_taken。
const AdoptedDefault = "セッションの終了で未完了のまま"

// Tasks はメモを置くときと、タスクをメモへ移すときに触るタスクの記録 (tasklog.Store が満たす)。
type Tasks interface {
	ActiveTaskID(sessionID string) string
	Get(sessionID, taskID string) (tasklog.Task, bool)
	Remove(sessionID, taskID string) error
}

// Park は cwd のセッションが置いたメモを、置き先 (repo、そのとき進行中のタスク) を決めてから出自 placed で add する。
// repo は依存図の repo と同じ鍵 (main worktree のパス、git の外なら cwd)。
// 外から置く入口は Park と Adopt だけにし、repo の決め方を呼び出し側に任せない。
func (s Store) Park(req Request, cwd string, tasks Tasks, now time.Time) (Added, error) {
	src := Source{SessionID: req.SessionID, TaskID: tasks.ActiveTaskID(req.SessionID), CWD: cwd, At: now}
	return s.add(gitrepo.RepoKey(cwd), req.Entry, OriginPlaced, src)
}

// Adopt は終了したセッションに残った未完了のタスクを、出自 adopted、種類 scope のメモにし、タスクの記録を消す。
// 種類を scope にするのは、残ったタスクを拾うかがまさに範囲の相談で、既定 (default_taken) が「未完了のまま」
// (やらないで進んだ) になるため。
// exited はセッションが終了したと確かめる (daemonstate.SessionExited。タスクの記録の時刻より後の pane の一覧に無いこと)。
// 確かめられなければメモを書かず記録も消さない。pane が exited と描いた後に daemon が止まっていても、
// 生きているセッションの記録を消さないため (models/handoff の LiveTaskKept)。
func (s Store) Adopt(sessionID, taskID string, tasks Tasks, exited func(sessionID string, after time.Time) error, now time.Time) (Added, error) {
	t, ok := tasks.Get(sessionID, taskID)
	if !ok {
		return Added{}, fmt.Errorf("タスク %s/%s が無い", sessionID, taskID)
	}
	if t.Status == "completed" {
		return Added{}, fmt.Errorf("タスク %s/%s は完了している", sessionID, taskID)
	}
	if err := exited(sessionID, t.UpdatedAt); err != nil {
		return Added{}, fmt.Errorf("タスク %s/%s は移さない: %w", sessionID, taskID, err)
	}
	e := Entry{Kind: KindScope, Title: t.Subject, Detail: t.Description, DefaultTaken: AdoptedDefault}
	added, err := s.add(gitrepo.RepoKey(t.CWD), e, OriginAdopted, Source{SessionID: sessionID, TaskID: taskID, CWD: t.CWD, At: now})
	if err != nil {
		return Added{}, err
	}
	// メモを書いてから消す。消し損ねてもメモは残り、タスクは保持期間で消える。
	return added, tasks.Remove(sessionID, taskID)
}
