package parking

import (
	"fmt"
	"time"

	"github.com/usadamasa/agents-daemon/internal/gitrepo"
	"github.com/usadamasa/agents-daemon/internal/tasklog"
)

// AdoptedDefault は Adopt で移したメモの default_taken。
const AdoptedDefault = "セッションの終了で未完了のまま"

// Park は cwd のセッションが置いた件を、置き先 (domain、repo、そのとき進行中のタスク) を決めてから add する。
// repo はボードの行と同じ鍵 (main worktree のパス、git の外なら cwd)。kind task は Adopt だけが置く。
// 外から置く入口は Park と Adopt だけにし、domain と repo の決め方を呼び出し側に任せない。
func (s Store) Park(req Request, cwd string, p tasklog.Placer, now time.Time) (Added, error) {
	if req.Kind == KindTask {
		return Added{}, fmt.Errorf("kind %q はボードからタスクを移すときだけ使う", KindTask)
	}
	d := p.Domain(req.SessionID, cwd)
	src := Source{SessionID: req.SessionID, TaskID: p.Tasks.ActiveTaskID(d, req.SessionID), CWD: cwd, At: now}
	return s.add(d, gitrepo.RepoKey(cwd), req.Entry, src)
}

// Adopt は終了したセッションに残った未完了のタスクを kind task のメモにし、タスクの記録を消す。
// viewer と cwd はボードを開いたセッションで、その domain の側にあるタスクだけを移す (私用と業務を混ぜない)。
// セッションが終了したかは見ない。ボードが終了したセッションのカードにだけ操作を出す。
func (s Store) Adopt(viewer, cwd, sessionID, taskID string, p tasklog.Placer, now time.Time) (Added, error) {
	d := p.Domain(viewer, cwd)
	t, ok := p.Tasks.Get(d, sessionID, taskID)
	if !ok {
		return Added{}, fmt.Errorf("%s のタスク %s/%s が無い", d, sessionID, taskID)
	}
	if t.Status == "completed" {
		return Added{}, fmt.Errorf("タスク %s/%s は完了している", sessionID, taskID)
	}
	e := Entry{Kind: KindTask, Title: t.Subject, Detail: t.Description, DefaultTaken: AdoptedDefault}
	added, err := s.add(d, gitrepo.RepoKey(t.CWD), e, Source{SessionID: sessionID, TaskID: taskID, CWD: t.CWD, At: now})
	if err != nil {
		return Added{}, err
	}
	// メモを書いてから消す。消し損ねてもメモは残り、タスクは保持期間で消える。
	return added, p.Tasks.Remove(d, sessionID, taskID)
}
