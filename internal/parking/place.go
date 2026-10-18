package parking

import (
	"time"

	"github.com/usadamasa/agents-daemon/internal/domain"
	"github.com/usadamasa/agents-daemon/internal/gitrepo"
	"github.com/usadamasa/agents-daemon/internal/tasklog"
)

// Placer は cwd とセッションから、件の置き先 (domain、repo、そのとき進行中のタスク) を決める。
type Placer struct {
	Tasks tasklog.Store
	// Classify は cwd の domain を決める (config の homePaths / defaultDomain)。
	Classify func(cwd string) domain.Domain
}

// Domain はセッションの domain。業務に倒したセッションは、cwd が私用でも業務にする (ボードと同じ規則)。
func (p Placer) Domain(sessionID, cwd string) domain.Domain {
	return p.Tasks.SessionDomain(sessionID, p.Classify(cwd))
}

// Park は cwd のセッションが置いた件を、置き先を決めてから Add する。repo はボードの行と同じ鍵
// (main worktree のパス、git の外なら cwd)。
func (s Store) Park(req Request, cwd string, p Placer, now time.Time) (Added, error) {
	d := p.Domain(req.SessionID, cwd)
	src := Source{SessionID: req.SessionID, TaskID: p.Tasks.ActiveTaskID(d, req.SessionID), CWD: cwd, At: now}
	return s.Add(d, gitrepo.RepoKey(cwd), req.Entry, src)
}
