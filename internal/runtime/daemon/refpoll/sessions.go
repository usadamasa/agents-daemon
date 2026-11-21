package refpoll

import (
	"github.com/usadamasa/agents-daemon/internal/applog"
	"github.com/usadamasa/agents-daemon/internal/worklog"
)

// WorkSessions は New に渡す sessions と issuers を、作業の記録 (worklog) と tick で見えたセッションから組む。
type WorkSessions struct {
	Works worklog.Store
	// Places は最後の tick で pane が見えたセッションと pane の cwd を返す。
	Places func() []Session
	Log    *applog.Logger
}

// Live は最後の tick で pane が見えたセッションとその今の cwd。pane の cwd は起動したディレクトリのままで、
// セッションの途中で入った worktree を映さないので、作業の記録の cwd (hook が渡す今の cwd) があればそちらを使う。
// cc-premise: 2.1.323 hooks EnterWorktree の後の hook の入力の cwd は worktree (rex の pane の cwd は起動したディレクトリのまま)
func (w WorkSessions) Live() []Session {
	places := w.Places()
	sessions := make([]Session, 0, len(places))
	for _, s := range places {
		if rec, ok := w.Works.Get(s.ID); ok && rec.CWD != "" {
			s.CWD, s.Repo = rec.CWD, rec.Repo
		}
		sessions = append(sessions, s)
	}
	return sessions
}

// Issuers は PR の参照ごとに、作業の記録 (prs) にその PR を出したと残したセッションと記録の cwd と repo の鍵を返す。
// 終了したセッションの記録も含む (記録は終了から worklog.Retention のあいだ残る)。
func (w WorkSessions) Issuers() map[string][]Session {
	recs, err := w.Works.List()
	if err != nil {
		w.Log.Logf("作業の記録を読めないので、PR を出した記録で辺を導きません: %v", err)
		return nil
	}
	issued := map[string][]Session{}
	for _, rec := range recs {
		for _, pr := range rec.Prs {
			issued[pr] = append(issued[pr], Session{ID: rec.SessionID, CWD: rec.CWD, Repo: rec.Repo})
		}
	}
	return issued
}
