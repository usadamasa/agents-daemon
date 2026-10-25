package board

import (
	"encoding/json"
	"io"

	"github.com/usadamasa/agents-daemon/internal/apppath"
	"github.com/usadamasa/agents-daemon/internal/daemon/state"
	"github.com/usadamasa/agents-daemon/internal/domain"
	"github.com/usadamasa/agents-daemon/internal/edge"
	"github.com/usadamasa/agents-daemon/internal/gitrepo"
	"github.com/usadamasa/agents-daemon/internal/parking"
	"github.com/usadamasa/agents-daemon/internal/sessioncontext"
	"github.com/usadamasa/agents-daemon/internal/tasklog"
)

// Viewer はボードを開いたセッション。ボードはこのセッションの domain を描く。
type Viewer struct {
	SessionID string
	CWD       string
	// UserHome は config の homePaths の ~ を展開する先。
	UserHome string
}

// Write はボードを組み、JSON で w へ書く。daemon が動いていなくてもタスクの記録は描く
// (daemon_running が false で、カードはすべて Live でない)。
func Write(w io.Writer, p apppath.Paths, v Viewer) error {
	pid, snap, err := state.Running(p)
	if err != nil {
		return err
	}
	placer := tasklog.LoadPlacer(p, v.UserHome)
	store := placer.Tasks
	d := placer.Domain(v.SessionID, v.CWD)
	tasks, err := store.List(d)
	if err != nil {
		return err
	}
	todos, err := store.ListTodos(d)
	if err != nil {
		return err
	}
	memos, err := parking.New(p.StateDir()).Find(d, "", "")
	if err != nil {
		return err
	}
	edges, err := edge.New(p.StateDir()).Find(d, "", "")
	if err != nil {
		return err
	}
	names, err := sessioncontext.New(p.StateDir()).ListSessionNames()
	if err != nil {
		return err
	}
	b := build(input{
		Domain:       d,
		CWD:          v.CWD,
		PID:          pid,
		Snapshot:     snap,
		Tasks:        tasks,
		Todos:        todos,
		Parking:      memos,
		Edges:        edges,
		Classify:     placer.Classify,
		Elsewhere:    func(id string) bool { return store.HasSession(other(d), id) },
		Locate:       gitrepo.Locate,
		SessionNames: names,
	})
	return json.NewEncoder(w).Encode(b)
}

// ViewerDomain はボードを開いたセッションの domain (Write が描く domain と同じ)。`logs --board` が読むログを
// 決めるのに使う。
func ViewerDomain(p apppath.Paths, v Viewer) domain.Domain {
	return tasklog.LoadPlacer(p, v.UserHome).Domain(v.SessionID, v.CWD)
}

func other(d domain.Domain) domain.Domain {
	if d == domain.Home {
		return domain.Work
	}
	return domain.Home
}
