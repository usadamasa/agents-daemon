// Package watch は rex の event の購読で、shell の block の状態の cache (blockstate.Cache) を保つ。購読を張ってから
// 全列挙し、全列挙の後に購読の event を当て続ける。購読が切れたら同期を外し、間隔を空けて張り直す。
// 購読の張り方と、全列挙・作られた block の埋め方は呼び手 (rex) が関数で渡す。
package watch

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/usadamasa/agents-daemon/internal/multiplexer"
	"github.com/usadamasa/agents-daemon/internal/runtime/backend/rex/api"
	"github.com/usadamasa/agents-daemon/internal/runtime/backend/rex/blockstate"
)

const (
	// defaultBackoff は購読が切れてから張り直すまでの最初の間隔。張り直すたびに倍にする。
	defaultBackoff = time.Second
	// maxBackoff は張り直しの間隔の上限。rex の server の再起動 (更新) は数秒で戻る。
	maxBackoff = 30 * time.Second
	// healthy はこれより長く続いた購読の後は間隔を最初に戻す。
	healthy = time.Minute
)

// Reads は cache を埋める読み取り。
type Reads struct {
	// Snapshot は全 session の shell の block の状態を読む。
	Snapshot func(ctx context.Context) (map[string]*blockstate.State, error)
	// Fill は作られた block (ref.Block が空なら session の全 block) のうち shell のものの状態を読む。
	// もう無い block と session は入れずに返す。
	Fill func(ctx context.Context, ref blockstate.Ref) (map[string]*blockstate.State, error)
}

// Watcher は購読と、それで保つ cache。nil の Watcher (購読していない) は常に未同期。
type Watcher struct {
	subscribe func(context.Context) (*api.Subscription, error)
	reads     Reads
	backoff   time.Duration

	mu sync.Mutex
	// cache が nil なら未同期で、呼び手は全列挙する。
	cache *blockstate.Cache
}

// New は Watcher を返す。backoff は張り直しの最初の間隔で、0 以下なら 1 秒。購読は Run で始める。
func New(subscribe func(context.Context) (*api.Subscription, error), reads Reads, backoff time.Duration) *Watcher {
	if backoff <= 0 {
		backoff = defaultBackoff
	}
	return &Watcher{subscribe: subscribe, reads: reads, backoff: backoff}
}

// Panes は同期済みなら cache の Pane を block ID の順に返す。sessions は block ID から session_id への対応。
func (w *Watcher) Panes(sessions map[string]string) ([]multiplexer.Pane, bool) {
	if w == nil {
		return nil, false
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.cache == nil {
		return nil, false
	}
	return w.cache.Panes(sessions), true
}

func (w *Watcher) set(cache *blockstate.Cache) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.cache = cache
}

// Run は背後で購読し、ctx が終わるまで切れるたびに張り直す。呼んだらすぐ戻る。返す channel は購読の接続を閉じ終えたら
// 閉じる。購読の失敗と張り直しは logf に書く。
func (w *Watcher) Run(ctx context.Context, logf func(format string, args ...any)) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		w.loop(ctx, logf)
	}()
	return done
}

func (w *Watcher) loop(ctx context.Context, logf func(format string, args ...any)) {
	backoff := w.backoff
	for {
		started := time.Now()
		err := w.once(ctx)
		w.set(nil)
		if ctx.Err() != nil {
			return
		}
		if time.Since(started) >= healthy {
			backoff = w.backoff
		}
		logf("rex の event の購読が切れました。%s 後に張り直し、それまで pane の一覧は全列挙で取ります: %v", backoff, err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, max(maxBackoff, w.backoff))
	}
}

// once は購読を 1 本張り、切れるまで cache を保つ。
//
// 全列挙は購読を張った (attach_all が応えた) 後に打ち、それまでに溜まった event を全部、全列挙の後に順に当てる。
// 購読は欠けず順序を保つので、全列挙より古い event が当たっても、その後の変化の event が後ろに必ずあり、cache は
// 最新に追いつく。全列挙の後に読んだ変化を 2 度当てても害は無い (record と前景は置き換え、close は消すだけ)。
// 全列挙が読んだ変化の event がまだ届いていない間に公開すると、その間だけ全列挙より古い状態が見えるので、全列挙の後に
// 購読の接続で Sync を打ち、その seq までの event を当て終えてから公開する。
func (w *Watcher) once(ctx context.Context) error {
	stream, err := w.subscribe(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = stream.Close() }()

	blocks, err := w.reads.Snapshot(ctx)
	if err != nil {
		return fmt.Errorf("購読を始めるときの全列挙に失敗: %w", err)
	}
	upTo, err := stream.Sync(ctx)
	if err != nil {
		return fmt.Errorf("購読を始めるときの同期に失敗: %w", err)
	}
	cache := blockstate.NewCache(blocks)
	var seen int64
	for seen < upTo {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ev, ok := <-stream.Events():
			if !ok {
				return ended(stream)
			}
			seen = ev.Seq
			if err := w.apply(ctx, cache, nil, ev.Payload); err != nil {
				return err
			}
		}
	}
	w.set(cache)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ev, ok := <-stream.Events():
			if !ok {
				return ended(stream)
			}
			if err := w.apply(ctx, cache, &w.mu, ev.Payload); err != nil {
				return err
			}
		}
	}
}

// ended は購読の接続が終わった理由。
func ended(stream *api.Subscription) error {
	return fmt.Errorf("購読の接続が終わった: %w", stream.Err())
}

// apply は event 1 件を cache に当て、block_created / session_created なら block を読んで埋める。mu があれば cache に
// 触る間だけ持つ (rex を読む間は離し、PaneList を待たせない)。この goroutine だけが cache を書くので、離した間に
// cache は変わらない。
func (w *Watcher) apply(ctx context.Context, cache *blockstate.Cache, mu *sync.Mutex, ev []byte) error {
	lock := func() {}
	unlock := func() {}
	if mu != nil {
		lock, unlock = mu.Lock, mu.Unlock
	}
	lock()
	ref, err := cache.Apply(ev)
	unlock()
	if err != nil || ref == (blockstate.Ref{}) {
		return err
	}
	created, err := w.reads.Fill(ctx, ref)
	if err != nil {
		return fmt.Errorf("作られた block (session %s、block %q) を埋められない: %w", ref.Session, ref.Block, err)
	}
	lock()
	for id, st := range created {
		cache.Put(blockstate.Ref{Session: ref.Session, Block: id}, st)
	}
	unlock()
	return nil
}
