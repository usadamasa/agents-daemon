package daemon

import (
	"time"

	"github.com/usadamasa/agents-daemon/internal/daemonstate"
	"github.com/usadamasa/agents-daemon/internal/ledger"
	"github.com/usadamasa/agents-daemon/internal/runtime/daemon/claudesessions"
)

// attachClaudeConfigDir は台帳に書く名前を引く Claude Code のセッションの登録簿を配線する。
func (r *daemonRuntime) attachClaudeConfigDir(dir string) {
	r.claudeSessions = claudesessions.New(dir)
}

// observeSessions は tick で見えたセッションを、pane が見えている間進める記録 (台帳、作業の記録) へ反映する。
func (r *daemonRuntime) observeSessions(panes []daemonstate.PaneStatus, now time.Time) {
	r.recordSessions(panes, now)
	r.touchWorks(panes, now)
}

// touchWorks は tick で見えたセッションの作業の記録の mtime を進める。開いたまま何も打たないセッションの記録を
// 保持期間で消さないため。進められなくても tick は止めない。
func (r *daemonRuntime) touchWorks(panes []daemonstate.PaneStatus, now time.Time) {
	ids := make([]string, 0, len(panes))
	for _, p := range panes {
		if p.SessionID != "" {
			ids = append(ids, p.SessionID)
		}
	}
	if err := r.works.Touch(ids, now); err != nil {
		r.log.Logf("作業の記録の mtime を進められません: %v", err)
	}
}

// recordSessions は tick で見えたセッションを台帳に書き、同じ tick で辺の session:<名前> の端点を台帳の
// セッションに結びつける。名前は ListAgents の名前 (Claude Code の登録簿) で、session:<名前> は model が
// SendMessage の宛先と同じ名前で書く。結びつけを tick ごとにするのは、渡した側が `edge add` を書いてから数秒で
// 依存図の端点がそのセッションのノードにつながり、pane が置き場のノードを描き続けないようにするため。台帳を
// 書いた直後に読むので、同じ tick で現れたセッションにも結びつく。書けなくても tick は止めない。
func (r *daemonRuntime) recordSessions(panes []daemonstate.PaneStatus, now time.Time) {
	if r.sessions.Dir == "" {
		return
	}
	names, err := r.claudeSessions.Names()
	if err != nil {
		r.log.Logf("セッション名の読み込みに失敗、名前無しで台帳を書きます: %v", err)
	}
	seen := make([]ledger.Seen, 0, len(panes))
	for _, p := range panes {
		if p.SessionID != "" {
			seen = append(seen, ledger.Seen{SessionID: p.SessionID, CWD: p.CWD, Name: names[p.SessionID]})
		}
	}
	if err := r.sessions.Observe(seen, now); err != nil {
		r.log.Logf("セッション台帳の書き込みに失敗: %v", err)
	}
	sessions, err := r.sessions.List()
	if err != nil {
		r.log.Logf("セッション台帳の読み込みに失敗、辺を結びつけません: %v", err)
		return
	}
	bound, err := r.edges.Bind(func(name string, createdAt time.Time) (string, bool) {
		return ledger.Pick(sessions, name, createdAt)
	})
	if err != nil {
		r.log.Logf("辺の端点の結びつけでエラー: %v", err)
	}
	for _, e := range bound {
		r.log.Logf("辺 %s の端点を結びつけました: %s → %s", e.ID, e.Before.Ref, e.After.Ref)
	}
}
