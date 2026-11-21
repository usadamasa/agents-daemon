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
	"github.com/usadamasa/agents-daemon/internal/runtime/backend/rex/wire"
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
	// Snapshot は全 session の shell の block の状態と view を読んだ cache を返す。
	Snapshot func(ctx context.Context) (*blockstate.Cache, error)
	// Fill は作られた block (ref.Block が空なら session の全 block) のうち shell のものの状態を読む。
	// もう無い block と session は入れずに返す。
	Fill func(ctx context.Context, ref blockstate.Ref) (map[string]*blockstate.State, error)
	// View は session の view を読む。読めなければ ok が false で、cache の view は変えない。閉じた session でない
	// 読み取りの失敗は failure に返り、cache が読めなかった理由として持つ。
	View func(ctx context.Context, session string) (view wire.SessionView, ok bool, failure error)
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

	cache, err := w.reads.Snapshot(ctx)
	if err != nil {
		return fmt.Errorf("購読を始めるときの全列挙に失敗: %w", err)
	}
	upTo, err := stream.Sync(ctx)
	if err != nil {
		return fmt.Errorf("購読を始めるときの同期に失敗: %w", err)
	}
	// 公開前の cache は誰も読まないので、held にして lock を取らずに当てる。
	r := &run{w: w, stream: stream, cache: cache, held: true}
	if err := r.drain(ctx, upTo); err != nil {
		return err
	}
	w.set(cache)
	r.held = false

	for {
		ev, err := r.next(ctx)
		if err != nil {
			return err
		}
		if err := r.apply(ctx, ev.Payload); err != nil {
			return err
		}
	}
}

// run は購読 1 本の間の cache の当て方。購読の goroutine だけが使う。
type run struct {
	w      *Watcher
	stream *api.Subscription
	cache  *blockstate.Cache
	// seen は当てた最後の event の seq。
	seen int64
	// held なら w.mu を既に持っているか cache が未公開で、cache に触るときに lock を取らない。
	held bool
}

// hold は w.mu を持ち (held なら取らない)、held にする。返す関数で元に戻す。
func (r *run) hold() func() {
	if r.held {
		return func() {}
	}
	r.w.mu.Lock()
	r.held = true
	return func() {
		r.held = false
		r.w.mu.Unlock()
	}
}

// next は購読の次の event を待つ。
func (r *run) next(ctx context.Context) (api.Event, error) {
	select {
	case <-ctx.Done():
		return api.Event{}, ctx.Err()
	case ev, ok := <-r.stream.Events():
		if !ok {
			return api.Event{}, fmt.Errorf("購読の接続が終わった: %w", r.stream.Err())
		}
		r.seen = ev.Seq
		return ev, nil
	}
}

// drain は seq が upTo の event まで当てる。
func (r *run) drain(ctx context.Context, upTo int64) error {
	for r.seen < upTo {
		ev, err := r.next(ctx)
		if err != nil {
			return err
		}
		if err := r.apply(ctx, ev.Payload); err != nil {
			return err
		}
	}
	return nil
}

// apply は event 1 件を cache に当て、block_created / session_created なら block を読んで埋め、session_created と
// 新しい session_view_changed なら session の view を読み直す。w.mu は cache に触る間だけ持つ (rex を読む間は離し、
// PaneList を待たせない。Fill の後の drain の中では持ったまま読む)。この goroutine だけが cache を書くので、離した間に
// cache は変わらない。
//
// Fill が読んだ変化の event は Fill の応答より後に届くことがあり、埋めた状態にそれより古い event が当たると、後の
// event が届くまで Fill より古い状態が見える。全列挙と同じく、Fill の後に Sync を打ち、埋めてからその seq までの
// event を w.mu を持ったまま当てて公開する。
func (r *run) apply(ctx context.Context, ev []byte) error {
	release := r.hold()
	ref, err := r.cache.Apply(ev)
	release()
	if err != nil || ref == (blockstate.Ref{}) {
		return err
	}
	if !ref.View {
		if err := r.fill(ctx, ref); err != nil {
			return err
		}
	}
	if ref.View || ref.Block == "" {
		view, ok, failure := r.w.reads.View(ctx, ref.Session)
		release := r.hold()
		if ok {
			r.cache.PutView(ref.Session, view)
		}
		if failure != nil {
			r.cache.PutViewFailure(ref.Session, failure)
		}
		release()
	}
	return nil
}

// fill は作られた block を読んで埋め、Fill が読んだ変化の event を当て終えてから w.mu を離す。
func (r *run) fill(ctx context.Context, ref blockstate.Ref) error {
	created, err := r.w.reads.Fill(ctx, ref)
	if err != nil {
		return fmt.Errorf("作られた block (session %s、block %q) を埋められない: %w", ref.Session, ref.Block, err)
	}
	if len(created) == 0 {
		return nil
	}
	upTo, err := r.stream.Sync(ctx)
	if err != nil {
		return fmt.Errorf("作られた block (session %s、block %q) を埋めた後の同期に失敗: %w", ref.Session, ref.Block, err)
	}
	release := r.hold()
	defer release()
	for id, st := range created {
		r.cache.Put(blockstate.Ref{Session: ref.Session, Block: id}, st)
	}
	return r.drain(ctx, upTo)
}
