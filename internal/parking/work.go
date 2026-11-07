package parking

import (
	"fmt"
	"time"

	"github.com/usadamasa/agents-daemon/internal/gitrepo"
	"github.com/usadamasa/agents-daemon/internal/worklog"
)

// Works はセッションの作業の記録 (worklog.Store が満たす)。タスクの無いセッションの作業をメモへ移すときに触る。
type Works interface {
	Get(sessionID string) (worklog.Record, bool)
	Remove(sessionID string) error
}

// AdoptWork は終了したセッションの作業 (タスクの無いセッションの記録、#368) を、Adopt と同じ出自 adopted、種類 scope の
// メモにし、作業の記録を消す。題は見せる題 (人が直した題、推定の題、最初の prompt の順)、detail は最後のターンの要約。
// exited に渡す時刻は作業の記録の updated_at (Stop ごとに進む)。確かめられなければメモを書かず記録も消さない
// (models/handoff の LiveTaskKept と同じ門)。
func (s Store) AdoptWork(sessionID string, works Works, exited func(sessionID string, after time.Time) error, now time.Time) (Added, error) {
	w, ok := works.Get(sessionID)
	if !ok {
		return Added{}, fmt.Errorf("セッション %s の作業の記録が無い", sessionID)
	}
	title := worklog.Title(w)
	if title == "" {
		return Added{}, fmt.Errorf("セッション %s の作業に題が無い (prompt を打っていない)", sessionID)
	}
	if err := exited(sessionID, w.UpdatedAt); err != nil {
		return Added{}, fmt.Errorf("セッション %s の作業は移さない: %w", sessionID, err)
	}
	e := Entry{Kind: KindScope, Title: title, Detail: w.Summary, DefaultTaken: AdoptedDefault}
	added, err := s.add(gitrepo.RepoKey(w.CWD), e, OriginAdopted, Source{SessionID: sessionID, CWD: w.CWD, At: now})
	if err != nil {
		return Added{}, err
	}
	// メモを書いてから消す。消し損ねてもメモは残り、記録は保持期間で消える。
	return added, works.Remove(sessionID)
}
