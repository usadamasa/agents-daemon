package deps

import (
	"encoding/json"
	"io"

	"github.com/usadamasa/agents-daemon/internal/apppath"
	"github.com/usadamasa/agents-daemon/internal/daemon/state"
	"github.com/usadamasa/agents-daemon/internal/edge"
	"github.com/usadamasa/agents-daemon/internal/gitrepo"
	"github.com/usadamasa/agents-daemon/internal/parking"
	"github.com/usadamasa/agents-daemon/internal/sessioncontext"
	"github.com/usadamasa/agents-daemon/internal/tasklog"
)

// Write は依存図を組み、JSON で w へ書く。cwd は開いたセッションの cwd (Deps.Here の元)。daemon が動いていなくても
// タスクの記録は描く (daemon_running が false で、ノードはすべて Live でない)。
func Write(w io.Writer, p apppath.Paths, cwd string) error {
	pid, snap, err := state.Running(p)
	if err != nil {
		return err
	}
	store := tasklog.New(p.StateDir())
	tasks, err := store.List()
	if err != nil {
		return err
	}
	todos, err := store.ListTodos()
	if err != nil {
		return err
	}
	memos, err := parking.New(p.StateDir()).Find("", "")
	if err != nil {
		return err
	}
	edges, err := edge.New(p.StateDir()).Find("", "")
	if err != nil {
		return err
	}
	names, err := sessioncontext.New(p.StateDir()).ListSessionNames()
	if err != nil {
		return err
	}
	b := build(input{
		CWD:          cwd,
		PID:          pid,
		Snapshot:     snap,
		Tasks:        tasks,
		Todos:        todos,
		Parking:      memos,
		Edges:        edges,
		Locate:       gitrepo.Locate,
		SessionNames: names,
	})
	return json.NewEncoder(w).Encode(b)
}
