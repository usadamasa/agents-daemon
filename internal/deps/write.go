package deps

import (
	"encoding/json"
	"io"

	"github.com/usadamasa/agents-daemon/internal/apppath"
	"github.com/usadamasa/agents-daemon/internal/daemonstate"
	"github.com/usadamasa/agents-daemon/internal/edge"
	"github.com/usadamasa/agents-daemon/internal/gitrepo"
	"github.com/usadamasa/agents-daemon/internal/ledger"
	"github.com/usadamasa/agents-daemon/internal/parking"
	"github.com/usadamasa/agents-daemon/internal/refstate"
	"github.com/usadamasa/agents-daemon/internal/sessioncontext"
	"github.com/usadamasa/agents-daemon/internal/tasklog"
	"github.com/usadamasa/agents-daemon/internal/worklog"
)

// Write は依存図を組み、JSON で w へ書く。読み手 (アプリ、handoff-notice の mod) はどのセッションにも属さないので、
// 開いたセッションの repo のような「自分」は持たない。daemon が動いていなくてもタスクの記録は描く (daemon_running が
// false で、ノードはすべて Live でない)。
func Write(w io.Writer, p apppath.Paths) error {
	pid, snap, err := daemonstate.Running(p)
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
	works, err := worklog.New(p.StateDir()).List()
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
	sessions, err := ledger.New(p.StateDir()).List()
	if err != nil {
		return err
	}
	refStore := refstate.New(p.StateDir())
	refs, err := refStore.States()
	if err != nil {
		return err
	}
	titles, err := refStore.Titles()
	if err != nil {
		return err
	}
	prs, err := refStore.PRs()
	if err != nil {
		return err
	}
	b := build(input{
		PID:          pid,
		Snapshot:     snap,
		Tasks:        tasks,
		Todos:        todos,
		Works:        works,
		Parking:      memos,
		Edges:        edges,
		Locate:       gitrepo.Locate,
		GitHubRepo:   gitrepo.GitHubRepo,
		SessionNames: names,
		Sessions:     sessions,
		RefStates:    refs,
		RefTitles:    titles,
		RefPRs:       prs,
	})
	return json.NewEncoder(w).Encode(b)
}
