// Package reaper は spawn と peer-sessions skill が起こした多重化ツールの session のうち、pane が無くなったものを
// daemon が閉じる (#557)。起こした claude の包みは、ほかの pane が残っていると自分では session を閉じないので、その
// pane を閉じた後に残る空の session をここで閉じる。起こした session は包みが書いた印 (spawnedsession) で見分け、
// 手で作った空の session は閉じない。daemon の tick ごとに Reap を 1 回呼ぶ。
package reaper

import (
	"context"
	"time"

	"github.com/usadamasa/agents-daemon/internal/applog"
	"github.com/usadamasa/agents-daemon/internal/spawnedsession"
)

// Interval は印を回る間隔。印 1 つにつき多重化ツールを 1 回叩くので tick (既定 5 秒) ごとには回らず、残った pane を
// 閉じてから session が消えるまでの待ちとして気にならない長さにする。
const Interval = 30 * time.Second

// Client は後片付けに使う多重化ツールの操作。rex の backend だけが満たし、daemon が型で確かめる。
type Client interface {
	// SessionBlocks は session の block を flavor を問わず数える。session が無ければ found=false。
	SessionBlocks(ctx context.Context, sessionID string) (n int, found bool, err error)
	// SessionKill は session を閉じる。
	SessionKill(ctx context.Context, sessionID string) error
}

// Reaper は印を回った時刻を daemon の寿命の間持つ。
type Reaper struct {
	marks  spawnedsession.Store
	client Client
	log    *applog.Logger
	// dryRun は数えるが閉じない。印も残し、dry-run を外した daemon に閉じさせる。
	dryRun bool
	lastAt time.Time
}

// New は marks の印を回り、client で数えて閉じる Reaper を作る。dryRun なら閉じずにログへ書くだけにする。
func New(marks spawnedsession.Store, client Client, log *applog.Logger, dryRun bool) *Reaper {
	return &Reaper{marks: marks, client: client, log: log, dryRun: dryRun}
}

// Reap は Interval ごとに印を回り、block が無くなった session を閉じて印を消す。判断は購読の cache (shell の block
// だけ) でなく、毎回数え直した block で行う。daemon が止まっていた間に空になった session も、起動後の最初の回で閉じる。
//   - block が残っていれば何もしない。
//   - session がもう無ければ (claude の包みが閉じた、人が閉じた) 印だけ消す。
//   - 数えられない、閉じられないときは印を残し、次の回に回す。
//
// nil の Reaper は何もしない。
func (p *Reaper) Reap(ctx context.Context, now time.Time) {
	if p == nil || (!p.lastAt.IsZero() && now.Before(p.lastAt.Add(Interval))) {
		return
	}
	p.lastAt = now
	recs, err := p.marks.List()
	if err != nil {
		p.log.Logf("起こした session の印を読めない: %v", err)
		return
	}
	for _, rec := range recs {
		p.reapOne(ctx, rec.SessionID)
	}
}

func (p *Reaper) reapOne(ctx context.Context, id string) {
	n, found, err := p.client.SessionBlocks(ctx, id)
	switch {
	case err != nil:
		p.log.Logf("起こした session %s の block を数えられない: %v", id, err)
		return
	case !found:
		p.remove(id)
		return
	case n > 0:
		return
	}
	if p.dryRun {
		p.log.Logf("[dry-run] pane が無くなった起こした session %s を閉じない", id)
		return
	}
	if err := p.client.SessionKill(ctx, id); err != nil {
		p.log.Logf("pane が無くなった起こした session %s を閉じられない: %v", id, err)
		return
	}
	p.log.Logf("pane が無くなった起こした session %s を閉じました", id)
	p.remove(id)
}

func (p *Reaper) remove(id string) {
	if err := p.marks.Remove(id); err != nil {
		p.log.Logf("起こした session %s の印を消せない: %v", id, err)
	}
}
