package rex

import (
	"context"
	"slices"

	"github.com/usadamasa/agents-daemon/internal/multiplexer"
	"github.com/usadamasa/agents-daemon/internal/runtime/backend/rex/blockstate"
	"github.com/usadamasa/agents-daemon/internal/runtime/backend/rex/watch"
)

// eventBuffer は購読の event の queue の長さ。全列挙と block の埋めの間に届く event を溜める。溢れたら購読を張り直す。
const eventBuffer = 1 << 14

var _ multiplexer.Watcher = (*execClient)(nil)

// Watch は rex の event を背後で購読し、PaneList が全列挙の代わりに cache を返すようにする (multiplexer.Watcher)。
// 呼んだらすぐ戻る。ctx が終わるまで、切れるたびに張り直す。購読の失敗と張り直しは logf に書く。
// PaneList と並ぶ前 (daemon の最初の tick より前) に 1 回だけ呼ぶ。
func (c *execClient) Watch(ctx context.Context, logf func(format string, args ...any)) <-chan struct{} {
	subscribe := func(ctx context.Context) (watch.Stream, error) { return c.rex.Subscribe(ctx, eventBuffer) }
	c.watch = watch.New(subscribe, watchSource{c}, c.watchBackoff)
	return c.watch.Run(ctx, logf)
}

// watchSource は購読の cache を rex の読み取りで埋める (watch.Source)。
type watchSource struct{ c *execClient }

func (s watchSource) Snapshot(ctx context.Context) (map[string]*blockstate.State, error) {
	return s.c.snapshot(ctx)
}

// Fill は作られた block のうち shell のものを program_status と process で埋める。ref.Block が空 (session_created) なら
// その session の shell の block を全部埋める。shell でない、もう閉じた block は入れない。
func (s watchSource) Fill(ctx context.Context, ref blockstate.Ref) (map[string]*blockstate.State, error) {
	shells, err := s.c.shellBlocks(ctx, ref.Session)
	if err != nil {
		if isNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	if ref.Block != "" {
		if !slices.Contains(shells, ref.Block) {
			return nil, nil
		}
		shells = []string{ref.Block}
	}
	created := map[string]*blockstate.State{}
	for _, id := range shells {
		st, err := s.c.fetchBlock(ctx, ref.Session, id)
		if isNotFound(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		created[id] = st
	}
	return created, nil
}
