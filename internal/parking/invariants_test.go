package parking

// メモの受け渡しの不変条件 (models/handoff の Handoff.tla) を、実物の Store を goroutine で交錯させて確かめる。
// 個々のテストが 1 場面ずつ固定するのに対し、ここでは「どの mod がどの順で取っても、送っても、捨てても破ってはいけないこと」を
// 書く。seed が固定するのは各 mod の選択 (どのメモを、取るか捨てるか、届くか) で、goroutine の交錯はスケジューラが決める。
// 全 mod が同じボード (open) を読み終えてから一斉に取りに行かせ、取り合いを毎回起こす。
// 反例が出たら seed と mod を出す。反例が出たらコードを直すか挙動を正当化する。性質を弱めて通さない。

import (
	"fmt"
	"math/rand/v2"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
)

const (
	handoffTraces      = 100
	handoffMaxAttempts = 3 // Handoff.cfg の MaxAttempts に合わせる
)

// handoffOutcome は取った後に起きること (Handoff.tla の Send / Spawn / Revert の枝)。
type handoffOutcome int

const (
	outcomeDelivered    handoffOutcome = iota // Send が届いた、または spawn で claude が前景になった
	outcomeSpawnTimeout                       // spawn で待ち切れた。pane は残るので戻さない
	outcomeReverted                           // 届かず、open に戻した
	outcomeRevertFailed                       // 届かず、戻しのプロセスも失敗した (handed のまま残る)
)

var handoffOutcomeNames = map[handoffOutcome]string{
	outcomeDelivered: "delivered", outcomeSpawnTimeout: "spawn-timeout", outcomeReverted: "reverted", outcomeRevertFailed: "revert-failed",
}

// memoCounts はメモ 1 つについて、テストの側で数えるもの (Handoff.tla の deliveries / sessions)。
type memoCounts struct {
	deliveries atomic.Int32
	sessions   atomic.Int32
}

// claimHolder は今メモを取っている mod。取れるのは 1 つの mod だけ (Claim の排他) を、ロックの外から見張る。
type claimHolder struct {
	mu     sync.Mutex
	holder map[string]int
}

// acquire は mod がメモ id を取った直後に呼ぶ。別の mod が取っていればその番号を返す。
func (h *claimHolder) acquire(id string, mod int) (int, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if other, held := h.holder[id]; held {
		return other, false
	}
	h.holder[id] = mod
	return 0, true
}

// release は revert (メモを open に戻す) を走らせ、戻せたら取っている印を外す。戻しと印の解除を同じロックの中で行うのは、
// 戻した直後にほかの mod が取ったとき、その acquire が印の解除より先に走って排他の破れと誤って数えないため。
func (h *claimHolder) release(id string, revert func() error) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := revert(); err != nil {
		return err
	}
	delete(h.holder, id)
	return nil
}

func (h *claimHolder) held(id string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	_, ok := h.holder[id]
	return ok
}

// handoffReached は乱択が通った経路。通っていなければ性質は何も確かめていない。
type handoffReached struct {
	mu       sync.Mutex
	outcomes map[handoffOutcome]int
	// claimLost は取りに行ったが先に取られていた (捨てられていた) 回数。0 なら取り合いが一度も起きていない。
	claimLost int
	discarded int
}

func (c *handoffReached) outcome(o handoffOutcome) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.outcomes[o]++
}

func (c *handoffReached) lost() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.claimLost++
}

func (c *handoffReached) discard() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.discarded++
}

func TestHandoff_不変条件を乱択の交錯で破らない(t *testing.T) {
	reached := &handoffReached{outcomes: map[handoffOutcome]int{}}
	defer func() {
		for o, name := range handoffOutcomeNames {
			if reached.outcomes[o] == 0 {
				t.Errorf("%s の経路を一度も通らなかった (入力の生成を見直す)", name)
			}
		}
		if reached.claimLost == 0 {
			t.Errorf("取り合いが一度も起きなかった (barrier を見直す)")
		}
		if reached.discarded == 0 {
			t.Errorf("Discard を一度も通らなかった")
		}
	}()
	for seed := range handoffTraces {
		r := rand.New(rand.NewPCG(uint64(seed), 0)) // #nosec G115 -- seed は非負
		s := New(t.TempDir())
		memos := make([]string, 1+r.IntN(3))
		for i := range memos {
			memos[i] = mustAdd(t, s, testRepo, aside("メモ "+strconv.Itoa(i)), source("s1", "")).ID
		}
		counts := map[string]*memoCounts{}
		for _, id := range memos {
			counts[id] = &memoCounts{}
		}
		holder := &claimHolder{holder: map[string]int{}}
		mods := 2 + r.IntN(3)
		var ready, done sync.WaitGroup
		start := make(chan struct{})
		failures := make(chan string, mods*handoffMaxAttempts)
		for mod := range mods {
			ready.Add(1)
			done.Add(1)
			go func() {
				defer done.Done()
				m := handoffMod{s: s, r: rand.New(rand.NewPCG(uint64(seed), uint64(mod+1))), mod: mod, memos: memos, counts: counts, holder: holder, reached: reached} // #nosec G115 -- seed と mod は非負
				m.run(func() { ready.Done(); <-start }, failures)
			}()
		}
		ready.Wait()
		close(start)
		done.Wait()
		close(failures)
		for msg := range failures {
			t.Fatalf("seed %d: %s", seed, msg)
		}
		for _, id := range memos {
			item, ok := readItem(filepath.Join(s.Dir, id+".json"))
			if !ok {
				t.Fatalf("seed %d: メモ %s を読めない", seed, id)
			}
			c := counts[id]
			if msg := checkHandoff(item, c, holder.held(id)); msg != "" {
				t.Fatalf("seed %d memo %s: %s\n  status=%s deliveries=%d sessions=%d", seed, id, msg, item.Status, c.deliveries.Load(), c.sessions.Load())
			}
		}
	}
}

// checkHandoff は 1 つのメモについて、全 mod が止まった後の不変条件を見て、破れていればその説明を返す。
func checkHandoff(item Item, c *memoCounts, held bool) string {
	switch {
	case c.deliveries.Load() > 1:
		return "AtMostOneDelivery: 1 つのメモが 2 つ以上の宛先に渡った"
	case c.sessions.Load() > 1:
		return "AtMostOneSession: 1 つのメモから 2 つ以上のセッションが起きた"
	case c.deliveries.Load() > 0 && item.Status != StatusHanded:
		return "HandedWhenDelivered: 届いたメモが handed のままでない"
	case item.Status == StatusDiscarded && (c.deliveries.Load() > 0 || c.sessions.Load() > 0):
		return "捨てたメモが届いた、またはセッションを起こした"
	case item.Status == StatusOpen && held:
		return "open のメモを取ったままの mod がいる"
	}
	return ""
}

// handoffMod はボードを開いた 1 つのセッションの mod (hooks/parking.ts の handOff と discard)。
type handoffMod struct {
	s       Store
	r       *rand.Rand
	mod     int
	memos   []string
	counts  map[string]*memoCounts
	holder  *claimHolder
	reached *handoffReached
}

// run は MaxAttempts 回まで、ボードを読んで open のメモを取りに行く。最初の読みの後は barrier で全 mod を揃える
// (Handoff.tla の Read → Claim)。破れを見つけたら failures に書く。
func (m *handoffMod) run(barrier func(), failures chan<- string) {
	for attempt := range handoffMaxAttempts {
		id := m.memos[m.r.IntN(len(m.memos))]
		view, err := m.s.Find("", StatusOpen)
		if err != nil {
			failures <- fmt.Sprintf("mod %d: Find() error = %v", m.mod, err)
			return
		}
		if attempt == 0 {
			barrier()
		}
		if !slices.ContainsFunc(view, func(it Item) bool { return it.ID == id }) {
			continue // 読んだボードで open でないメモには触らない
		}
		if m.r.IntN(5) == 0 {
			m.discard(id)
			continue
		}
		if _, _, err := m.s.Set(id, StatusHanded, StatusOpen); err != nil {
			m.reached.lost() // ほかの mod が先に取った、または捨てた
			continue
		}
		if other, ok := m.holder.acquire(id, m.mod); !ok {
			failures <- fmt.Sprintf("mod %d: mod %d が取っているメモ %s を取れた (Claim の排他が破れた)", m.mod, other, id)
			return
		}
		if msg := m.after(id); msg != "" {
			failures <- fmt.Sprintf("mod %d: %s", m.mod, msg)
			return
		}
	}
}

// discard は読んだボードで open だったメモを捨てる (Handoff.tla の Discard)。先に取られていれば失敗してよい。
func (m *handoffMod) discard(id string) {
	if _, _, err := m.s.Set(id, StatusDiscarded, StatusOpen); err != nil {
		m.reached.lost()
		return
	}
	m.reached.discard()
}

// after は取ったメモに起きることを 1 つ引いて写す。戻したときだけ取っている印を外す (release)。
func (m *handoffMod) after(id string) string {
	outcome := handoffOutcome(m.r.IntN(len(handoffOutcomeNames)))
	m.reached.outcome(outcome)
	switch outcome {
	case outcomeDelivered:
		m.counts[id].deliveries.Add(1)
		if m.r.IntN(2) == 0 { // spawn で届いた
			m.counts[id].sessions.Add(1)
		}
	case outcomeSpawnTimeout:
		m.counts[id].sessions.Add(1)
	case outcomeReverted:
		revert := func() error {
			_, _, err := m.s.Set(id, StatusOpen, StatusHanded)
			return err
		}
		if err := m.holder.release(id, revert); err != nil {
			return fmt.Sprintf("取ったメモ %s を戻せなかった: %v", id, err)
		}
	case outcomeRevertFailed:
	}
	return ""
}
