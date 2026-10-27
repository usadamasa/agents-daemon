package rexcli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/usadamasa/agents-daemon/internal/multiplexer"
)

const (
	// defaultWatchBackoff は購読が切れてから張り直すまでの最初の間隔。張り直すたびに倍にする。
	defaultWatchBackoff = time.Second
	// maxWatchBackoff は張り直しの間隔の上限。rex の server の再起動 (更新) は数秒で戻る。
	maxWatchBackoff = 30 * time.Second
	// watchHealthy はこれより長く続いた購読の後は間隔を最初に戻す。
	watchHealthy = time.Minute
	// firstLineWait は events を張ってから最初の行を待つ時間。過ぎたら 1 行起こしてもう一度待つ。
	firstLineWait = 2 * time.Second
	// streamQueue は読んだ行を当てる側へ渡す queue の長さ。全列挙の間に届く event を溜める。
	streamQueue = 4096
	// maxLineBytes は 1 行の上限。process_changed は ancestors を持つ。
	maxLineBytes = 1 << 20
)

var _ multiplexer.Watcher = (*execClient)(nil)

// watchState は購読の cache。cache が nil なら未同期で、PaneList は全列挙する。
type watchState struct {
	mu    sync.Mutex
	cache *paneCache
}

func (w *watchState) synced() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.cache != nil
}

// panes は同期済みなら cache の Pane を返す。nil の watchState (購読していない) は常に ok=false。
func (w *watchState) panes(sessions map[string]string) ([]multiplexer.Pane, bool) {
	if w == nil {
		return nil, false
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.cache == nil {
		return nil, false
	}
	return w.cache.panes(sessions), true
}

func (w *watchState) set(cache *paneCache) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.cache = cache
}

// Watch は rex の events を背後で購読し、PaneList が全列挙の代わりに cache を返すようにする (multiplexer.Watcher)。
// 呼んだらすぐ戻る。ctx が終わるまで、切れるたびに張り直す。購読の失敗と張り直しは logf に書く。
// PaneList と並ぶ前 (daemon の最初の tick より前) に 1 回だけ呼ぶ。
func (c *execClient) Watch(ctx context.Context, logf func(format string, args ...any)) {
	c.watch = &watchState{}
	go c.watchLoop(ctx, logf)
}

func (c *execClient) watchLoop(ctx context.Context, logf func(format string, args ...any)) {
	first := c.watchBackoff
	if first <= 0 {
		first = defaultWatchBackoff
	}
	backoff := first
	for {
		started := time.Now()
		err := c.watchOnce(ctx)
		c.watch.set(nil)
		if ctx.Err() != nil {
			return
		}
		if time.Since(started) >= watchHealthy {
			backoff = first
		}
		logf("rex events の購読が切れました。%s 後に張り直し、それまで pane の一覧は全列挙で取ります: %v", backoff, err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, max(maxWatchBackoff, first))
	}
}

// watchOnce は events を 1 本張り、切れるまで cache を保つ。戻るのは stream が切れたか ctx が終わったとき。
//
// 全列挙は events を張って最初の行を受けた後に打つ。stream は欠けず順序を保つので、全列挙の間に届いた event を
// 全列挙の後に順に当て直せば cache は最新に追いつく (snapshot より古い event が当たっても、その後の変化の event が
// 後ろに必ずある)。
func (c *execClient) watchOnce(ctx context.Context) error {
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stream, err := c.startEvents(streamCtx)
	if err != nil {
		return err
	}
	// 子を止めてから待つ。逆にすると流れ続ける stream の終わりを待ち続ける。
	defer func() {
		cancel()
		stream.close()
	}()

	if err := c.awaitFirstLine(streamCtx, stream); err != nil {
		return err
	}
	blocks, err := c.snapshot(streamCtx)
	if err != nil {
		return fmt.Errorf("購読を始めるときの全列挙に失敗: %w", err)
	}
	cache := newPaneCache(blocks)
	for drained := false; !drained; {
		select {
		case line, ok := <-stream.lines:
			if !ok {
				return stream.endErr()
			}
			if err := c.applyLine(streamCtx, cache, nil, line); err != nil {
				return err
			}
		default:
			drained = true
		}
	}
	c.watch.set(cache)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case line, ok := <-stream.lines:
			if !ok {
				return stream.endErr()
			}
			if err := c.applyLine(streamCtx, cache, &c.watch.mu, line); err != nil {
				return err
			}
		}
	}
}

// applyLine は 1 行を cache に当て、block_created なら block を確かめて埋める。mu があれば cache に触る間だけ持つ
// (rex を叩く間は離し、PaneList を待たせない)。この goroutine だけが cache を書くので、離した間に cache は変わらない。
func (c *execClient) applyLine(ctx context.Context, cache *paneCache, mu *sync.Mutex, line []byte) error {
	lock := func() {}
	unlock := func() {}
	if mu != nil {
		lock, unlock = mu.Lock, mu.Unlock
	}
	lock()
	ref, err := cache.apply(line)
	unlock()
	if err != nil || ref == (blockRef{}) {
		return err
	}
	st, err := c.fetchCreated(ctx, ref)
	if err != nil {
		return fmt.Errorf("作られた block %s を埋められない: %w", ref.block, err)
	}
	if st != nil {
		lock()
		cache.put(ref, st)
		unlock()
	}
	return nil
}

// fetchCreated は作られた block が shell なら program_status と process で埋める。shell でないか、もう閉じていれば nil。
func (c *execClient) fetchCreated(ctx context.Context, ref blockRef) (*blockState, error) {
	shells, err := c.shellBlocks(ctx, ref.session)
	if err != nil {
		if isTargetNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	if !slices.Contains(shells, ref.block) {
		return nil, nil
	}
	st, err := c.fetchBlock(ctx, ref.session, ref.block)
	if isTargetNotFound(err) {
		return nil, nil
	}
	return st, err
}

// awaitFirstLine は購読が始まった (server が event を流し始めた) ことを最初の行で確かめる。rex の CLI の呼び出しは
// それだけで client_joined を流すので、待っても来なければ 1 回呼んで起こす。
func (c *execClient) awaitFirstLine(ctx context.Context, stream *eventStream) error {
	for attempt := 0; ; attempt++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-stream.first:
			return nil
		case <-stream.done:
			return stream.endErr()
		case <-time.After(firstLineWait):
			if attempt > 0 {
				return fmt.Errorf("rex events が %s 待っても流れない", 2*firstLineWait)
			}
			if err := c.Reachable(ctx); err != nil {
				return err
			}
		}
	}
}

// eventStream は `events --all --json` の子プロセスと、その行。
type eventStream struct {
	cmd    *exec.Cmd
	stderr *bytes.Buffer
	// lines は購読に関わる行だけ。stdout が終わると閉じる。
	lines chan []byte
	// first は最初の行 (種類を問わない) を読んだら閉じる。done は stdout が終わったら閉じる。
	first chan struct{}
	done  chan struct{}
	// readErr は stdout の読み取りの失敗。done が閉じた後に読む。
	readErr error
}

// startEvents は events を起こす。覚えていた rex が消えていたら、次の張り直しで引き直させる。
// rex-premise: preview.21 cli events --all --json は全 session の block_event を 1 行 1 event で server が切れるまで流す
func (c *execClient) startEvents(ctx context.Context) (*eventStream, error) {
	bin, err := c.bin()
	if err != nil {
		return nil, err
	}
	// #nosec G204 -- bin は PATH か REX_BIN_PATH から引いた rex、引数は固定。
	cmd := exec.CommandContext(ctx, bin, "--autostart=false", "events", "--all", "--json")
	cmd.Env = childEnv()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("rex events: %w", err)
	}
	s := &eventStream{
		cmd:    cmd,
		stderr: &bytes.Buffer{},
		lines:  make(chan []byte, streamQueue),
		first:  make(chan struct{}),
		done:   make(chan struct{}),
	}
	cmd.Stderr = s.stderr
	cmd.WaitDelay = 100 * time.Millisecond
	if err := cmd.Start(); err != nil {
		if binaryVanished(err) {
			c.forgetBin(bin)
		}
		return nil, fmt.Errorf("rex events: %w", err)
	}
	go s.read(ctx, stdout)
	return s, nil
}

// read は stdout を行に分け、購読に関わる行だけを lines へ送る。client の出入りのような雑音は daemon 自身の
// 呼び出しのたびに流れるので、ここで落として queue を溢れさせない。ctx が終われば送らずに抜ける
// (受け手が居なくなった後に queue の空きを待ち続けない)。
func (s *eventStream) read(ctx context.Context, stdout io.Reader) {
	defer close(s.done)
	defer close(s.lines)
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), maxLineBytes)
	firstSeen := false
	for scanner.Scan() {
		if !firstSeen {
			firstSeen = true
			close(s.first)
		}
		if !relevantLine(scanner.Bytes()) {
			continue
		}
		select {
		case s.lines <- bytes.Clone(scanner.Bytes()):
		case <-ctx.Done():
			return
		}
	}
	s.readErr = scanner.Err()
}

// relevantLine は cache が読む type の行か。壊れた行は通し、apply に error を出させる。
func relevantLine(line []byte) bool {
	var l struct {
		Type    string `json:"type"`
		Payload struct {
			Event *struct {
				Type string `json:"type"`
			} `json:"event"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(line, &l); err != nil {
		return true
	}
	switch l.Type {
	case "session_destroyed":
		return true
	case "session_event":
		if l.Payload.Event == nil {
			return false
		}
		switch l.Payload.Event.Type {
		case "block_event", "block_created", "block_closed":
			return true
		}
	}
	return false
}

// endErr は stream が終わった理由。stdout の読み取りの失敗か、子プロセスの終わり方と stderr。
func (s *eventStream) endErr() error {
	<-s.done
	if s.readErr != nil {
		return fmt.Errorf("rex events の読み取りに失敗: %w", s.readErr)
	}
	err := s.cmd.Wait()
	stderr := strings.TrimSpace(s.stderr.String())
	if err == nil {
		return fmt.Errorf("rex events が終わった (stderr=%q)", stderr)
	}
	return fmt.Errorf("rex events が終わった: %w (stderr=%q)", err, stderr)
}

// close は子プロセスを待つ。ctx の cancel で kill した後に呼ぶ。endErr が先に Wait していれば何もしない。
func (s *eventStream) close() {
	<-s.done
	if s.cmd.ProcessState == nil {
		_ = s.cmd.Wait() // 止めた子の終わり方は切れた理由にならない
	}
}
