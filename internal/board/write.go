package board

import (
	"encoding/json"
	"io"

	"github.com/usadamasa/agents-daemon/internal/apppath"
	"github.com/usadamasa/agents-daemon/internal/config"
	"github.com/usadamasa/agents-daemon/internal/daemon"
	"github.com/usadamasa/agents-daemon/internal/domain"
	"github.com/usadamasa/agents-daemon/internal/gitrepo"
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
	pid, err := daemon.ReadPIDFile(p.PIDFile())
	if err != nil {
		return err
	}
	if !daemon.PIDAlive(pid) {
		pid = 0
	}
	var snap *daemon.StatusSnapshot
	if pid != 0 {
		if snap, err = daemon.ReadStatusSnapshot(p.StatusFile()); err != nil {
			return err
		}
	}
	// 設定が読めなくても既定 (すべて業務) で描く。業務のものが私用の側へ出ない向きに倒れる。
	cfg, _ := config.Load(p.ConfigFile())
	c := cfg.Classifier(v.UserHome)
	store := tasklog.New(p.StateDir())

	d := store.SessionDomain(v.SessionID, c.Classify(v.CWD))
	tasks, err := store.List(d)
	if err != nil {
		return err
	}
	todos, err := store.ListTodos(d)
	if err != nil {
		return err
	}
	b := Build(Input{
		Domain:    d,
		PID:       pid,
		Snapshot:  snap,
		Tasks:     tasks,
		Todos:     todos,
		Classify:  c.Classify,
		Elsewhere: func(id string) bool { return store.HasSession(other(d), id) },
		Locate:    gitrepo.Locate,
	})
	return json.NewEncoder(w).Encode(b)
}

func other(d domain.Domain) domain.Domain {
	if d == domain.Home {
		return domain.Work
	}
	return domain.Home
}
