package tasklog

import (
	"github.com/usadamasa/agents-daemon/internal/apppath"
	"github.com/usadamasa/agents-daemon/internal/config"
	"github.com/usadamasa/agents-daemon/internal/domain"
)

// Placer は cwd とセッションから domain を決める。ボード、パーキング、辺、タスクの記録が同じ規則で私用と業務を分ける。
type Placer struct {
	Tasks Store
	// Classify は cwd の domain を決める (config の homePaths / defaultDomain)。
	Classify func(cwd string) domain.Domain
}

// LoadPlacer は p の config とタスクの記録から Placer を組む。userHome は homePaths の ~ を展開する先。
func LoadPlacer(p apppath.Paths, userHome string) Placer {
	// 設定が読めなくても Load は既定値 (すべて業務) を返す。業務のものが私用の側へ出ない向きに倒れる。
	cfg, _ := config.Load(p.ConfigFile())
	return Placer{Tasks: New(p.StateDir()), Classify: domain.New(userHome, cfg.HomePaths, cfg.DefaultDomain).Classify}
}

// Domain はセッションの domain。そのセッションの記録が業務の側にあれば業務 (業務に倒したセッション)、
// 無ければ cwd で決める。業務のセッションの文面を私用の側へ出さないため。
func (p Placer) Domain(sessionID, cwd string) domain.Domain {
	if sessionID != "" && p.Tasks.HasSession(domain.Work, sessionID) {
		return domain.Work
	}
	return p.Classify(cwd)
}
