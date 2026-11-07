package deps

import (
	"github.com/usadamasa/agents-daemon/internal/daemonstate"
	"github.com/usadamasa/agents-daemon/internal/deps/graph"
	"github.com/usadamasa/agents-daemon/internal/multiplexer"
)

// activity は生きているセッションの pane からセッションの活動 (graph の StateWorking など) を導く。pane を解釈する
// のはここだけで、ノードの状態は graph がこの活動と作業の進みから決める。detail は上限の解除時刻。
// 上限待ちは agent の自己申告より先に見る。上限で止まった pane は Claude Code の申告では idle や error に見えるが、
// 利用者が一番知りたいのは「daemon が再開を待っている」ことなので。
// cc-premise: 2.1.302 osc7501 agent_status は idle / working / blocked / done / error、blocked の kind は permission / question / auth
func activity(p *daemonstate.PaneStatus) (act, detail string) {
	if p.MonitorStatus == "waiting" {
		if p.WaitUntil != nil {
			detail = p.WaitUntil.Local().Format("15:04")
		}
		return graph.StateLimit, detail
	}
	switch multiplexer.AgentStatus(p.AgentStatus) {
	case multiplexer.AgentStatusBlocked:
		// permission 以外の待ち (auth、kind の無い dialog) も人の手を待つものとして permission に寄せる。
		if p.AgentKind == "question" {
			return graph.StateQuestion, ""
		}
		return graph.StatePermission, ""
	case multiplexer.AgentStatusError:
		return graph.StateError, ""
	case multiplexer.AgentStatusDone, multiplexer.AgentStatusIdle:
		return graph.StateDone, ""
	case multiplexer.AgentStatusWorking:
		return graph.StateWorking, ""
	default:
		return graph.StateUnknown, ""
	}
}

// sessionActivity は session ID → 活動と補足と、Claude Code が申告した今の作業 (msg)。生きているセッションだけを持つ。
type sessionActivity struct {
	act, detail, msg string
}

// activities は生きているセッションごとに活動を 1 回だけ導く。
func activities(panes map[string]*daemonstate.PaneStatus) map[string]sessionActivity {
	out := make(map[string]sessionActivity, len(panes))
	for id, p := range panes {
		act, detail := activity(p)
		out[id] = sessionActivity{act: act, detail: detail, msg: p.AgentMessage}
	}
	return out
}

// summaryFor は status の作業に添える今の作業。セッションが今進めているのは進行中の作業だけなので、
// 未着手と完了の作業には添えない。タスクの subject と今の作業のずれに気付けるようにする (#157)。
// cc-premise: 2.1.302 osc7501 msg は作業中の作業、ターン後の要約、permission 待ちの approve <tool>: <command>
func (a sessionActivity) summaryFor(status string) string {
	if status != "in_progress" {
		return ""
	}
	return a.msg
}
