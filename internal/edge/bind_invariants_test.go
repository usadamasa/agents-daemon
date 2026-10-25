package edge

// 結びつけ (Bind) と edge add / edge rm (Add / Remove) を実物の Store で goroutine に交錯させ、models/handoff の
// EdgeBind の不変条件を確かめる。
//
//   - BindOnce: 結びついた端点は、辺が消されるまで別の値 (名前や別のセッション) に戻らない
//   - NoResurrect: Remove で消した辺を Bind の書き込みが作り直さない
//   - 書いたときの参照の組ごと、今の参照の組ごとに辺は 1 本
//
// Pick は呼ぶたびに別のセッションを返すので、結び直しがあれば値の違いとして見える。辺の見分けは id と created_at
// (消してから Add し直した辺は created_at が新しい)。seed が固定するのは各 goroutine の手で、交錯はスケジューラが
// 決める。反例が出たらコードを直すか挙動を正当化し、性質を弱めて通さない。

import (
	"fmt"
	"math/rand/v2"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const (
	bindTraces  = 15
	bindWriters = 2
	bindSteps   = 6
	bindRounds  = 8
)

// bindPairs は書く辺の参照の組。結びつける前の形と、結びついた後に書き手が使いうる形を混ぜる。
var bindPairs = [][2]string{
	{"session:a", "issue:o/r#1"},
	{"session:b", "issue:o/r#1"},
	{"session:a", "session:b"},
	{"sid:s1", "issue:o/r#1"},
	{"sid:s2", "session:b"},
}

func endpoints(pair [2]string) (Endpoint, Endpoint) {
	cwd := func(r string) string { return "/home/" + strings.NewReplacer(":", "-", "/", "-", "#", "-").Replace(r) }
	return Endpoint{Ref: pair[0], CWD: cwd(pair[0])}, Endpoint{Ref: pair[1], CWD: cwd(pair[1])}
}

// countingPick は呼ぶたびに新しいセッション (s1、s2、…) を返す。
func countingPick() Pick {
	var n atomic.Int64
	return func(string, time.Time) (string, bool) {
		return fmt.Sprintf("s%d", n.Add(1)), true
	}
}

// incarnation は辺 1 本の見分け (消してから書き直した辺は別)。
type incarnation struct {
	id      string
	created time.Time
}

func TestBind_交錯しても結び直さず辺は組ごとに1本(t *testing.T) {
	boundSome := false
	for seed := range bindTraces {
		s := New(t.TempDir())
		var clock atomic.Int64
		now := func() time.Time { return testNow.Add(time.Duration(clock.Add(1)) * time.Second) }
		for _, pair := range bindPairs[:3] {
			before, after := endpoints(pair)
			mustAdd(t, s, before, after, "", now())
		}
		failures := make(chan string, 64)
		var wg sync.WaitGroup
		stop := make(chan struct{})
		var mu sync.Mutex
		observed := map[incarnation][2]string{} // 見た端点のうち結びついたもの (before、after)
		// 読むのも mu の中で行う。2 つの観測者が外で読むと、先に読んだ古い写しを後から照合して結び直しと取り違える。
		observe := func() {
			mu.Lock()
			defer mu.Unlock()
			got, err := s.Find("", "")
			if err != nil {
				failures <- fmt.Sprintf("Find() error = %v", err)
				return
			}
			for _, e := range got {
				key := incarnation{e.ID, e.CreatedAt}
				prev := observed[key]
				for i, ep := range []Endpoint{e.Before, e.After} {
					switch {
					case prev[i] != "" && ep.Ref != prev[i]:
						failures <- fmt.Sprintf("辺 %s の端点 %d が %s に結びついた後 %s になった", e.ID, i, prev[i], ep.Ref)
					case ep.BoundFrom != "":
						prev[i] = ep.Ref
						boundSome = true
					}
				}
				observed[key] = prev
			}
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			pick := countingPick()
			for range bindRounds {
				if _, err := s.Bind(pick); err != nil {
					failures <- fmt.Sprintf("Bind() error = %v", err)
				}
				observe()
			}
		}()
		var observer sync.WaitGroup
		observer.Add(1)
		go func() {
			defer observer.Done()
			for {
				select {
				case <-stop:
					return
				default:
					observe()
				}
			}
		}()
		for writer := range bindWriters {
			wg.Add(1)
			go func() {
				defer wg.Done()
				wr := rand.New(rand.NewPCG(uint64(seed), uint64(writer+20))) // #nosec G115 -- seed と writer は非負
				for range bindSteps {
					before, after := endpoints(bindPairs[wr.IntN(len(bindPairs))])
					var err error
					if wr.IntN(4) == 0 {
						_, err = s.Remove(before, after)
					} else {
						_, err = s.Add(before, after, "w", now())
					}
					if err != nil {
						failures <- fmt.Sprintf("writer %d: %v", writer, err)
						return
					}
				}
			}()
		}
		wg.Wait()
		close(stop)
		observer.Wait()
		observe()
		close(failures)
		for msg := range failures {
			t.Fatalf("seed %d: %s", seed, msg)
		}
		if msg := checkPairs(s); msg != "" {
			t.Fatalf("seed %d: %s", seed, msg)
		}
	}
	if !boundSome {
		t.Errorf("一度も結びつかなかった (入力の生成を見直す)")
	}
}

// checkPairs は、書いたときの参照の組ごと、今の参照の組ごとに辺が 1 本で、id が書いたときの参照の組から決まることを見る。
func checkPairs(s Store) string {
	got, err := s.Find("", "")
	if err != nil {
		return fmt.Sprintf("Find() error = %v", err)
	}
	written, current := map[[2]string]bool{}, map[[2]string]bool{}
	for _, e := range got {
		w := [2]string{e.Before.written(), e.After.written()}
		c := [2]string{e.Before.Ref, e.After.Ref}
		switch {
		case e.ID != edgeID(w[0], w[1]):
			return fmt.Sprintf("辺 %s の id が書いたときの参照の組 %v から決まる値でない", e.ID, w)
		case written[w]:
			return fmt.Sprintf("書いたときの参照の組 %v の辺が 2 本ある", w)
		case current[c]:
			return fmt.Sprintf("今の参照の組 %v の辺が 2 本ある", c)
		case c[0] == c[1]:
			return fmt.Sprintf("自分自身への辺 %s がある", e.ID)
		}
		written[w], current[c] = true, true
	}
	return ""
}

func TestBind_交錯しても消した辺を作り直さない(t *testing.T) {
	removedSome := 0
	for seed := range bindTraces {
		s := New(t.TempDir())
		for i, pair := range bindPairs[:3] {
			before, after := endpoints(pair)
			mustAdd(t, s, before, after, "", testNow.Add(time.Duration(i)*time.Second))
		}
		var mu sync.Mutex
		removed := map[string]bool{}
		failures := make(chan string, 16)
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			pick := countingPick()
			for range bindRounds {
				if _, err := s.Bind(pick); err != nil {
					failures <- fmt.Sprintf("Bind() error = %v", err)
				}
			}
		}()
		wg.Add(1)
		go func() {
			defer wg.Done()
			wr := rand.New(rand.NewPCG(uint64(seed), 30)) // #nosec G115 -- seed は非負
			for range bindSteps {
				// 書いたときの参照の組で消す。Bind の前でも後でも当たる。
				before, after := endpoints(bindPairs[wr.IntN(3)])
				got, err := s.Remove(before, after)
				if err != nil {
					failures <- fmt.Sprintf("Remove() error = %v", err)
					return
				}
				if got.Removed {
					mu.Lock()
					removed[got.ID] = true
					removedSome++
					mu.Unlock()
				}
			}
		}()
		wg.Wait()
		close(failures)
		for msg := range failures {
			t.Fatalf("seed %d: %s", seed, msg)
		}
		left, err := s.Find("", "")
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range left {
			if removed[e.ID] {
				t.Fatalf("seed %d: 消した辺 %s が残った: %+v", seed, e.ID, e)
			}
		}
	}
	if removedSome == 0 {
		t.Errorf("Remove が一度も辺を消さなかった (入力の生成を見直す)")
	}
}
