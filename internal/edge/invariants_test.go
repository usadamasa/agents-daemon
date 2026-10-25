package edge

// 辺の置き場が交錯しても破ってはいけないことを、実物の Store を goroutine で交錯させて確かめる。辺には models/handoff の
// 不変条件は無い (受け渡しに絡まない) ので、ここで見るのは置き場の形: 参照の組ごとにファイルは 1 つで id はその組から
// 決まる、残っている辺は書いた辺のどれかそのもの、Find は端点の一致で絞る。同じ辺の Add が重なると created_at は後から
// 書いた側になりうる (ロックが無い) ので、created_at の保持は 1 本ずつ書く場合 (edge_test.go) にだけ求める。
// seed が固定するのは各 goroutine の手で、交錯はスケジューラが決める。反例が出たらコードを直すか挙動を正当化する。

import (
	"fmt"
	"math/rand/v2"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// Add は端点ごとに git を起こす (gitrepo.RepoKey) ので、回数は monitor や parking より少なめにする。
const (
	edgeTraces  = 20
	edgeWriters = 3
	edgeSteps   = 5
)

// edgeRefs は端点に使う参照。
var edgeRefs = []string{"session:a", "session:b", "issue:usadamasa/agents-daemon#1", "task:s1/2"}

func TestStore_交錯しても参照の組ごとに1つの辺になる(t *testing.T) {
	removedSome := 0
	defer func() {
		if removedSome == 0 {
			t.Errorf("Remove が一度も辺を消さなかった (入力の生成を見直す)")
		}
	}()
	for seed := range edgeTraces {
		s := New(t.TempDir())
		var mu sync.Mutex
		written := map[string]Edge{} // id ごとに、書いた辺の候補のうち最後に見たもの (note で書いた側が分かる)
		var ready, done sync.WaitGroup
		start := make(chan struct{})
		failures := make(chan string, edgeWriters*edgeSteps)
		for writer := range edgeWriters {
			ready.Add(1)
			done.Add(1)
			go func() {
				defer done.Done()
				wr := rand.New(rand.NewPCG(uint64(seed), uint64(writer+10))) // #nosec G115 -- seed と writer は非負
				ready.Done()
				<-start
				for step := range edgeSteps {
					before, after := pick(wr)
					if before.Ref == after.Ref {
						continue
					}
					if wr.IntN(3) == 0 {
						removed, err := s.Remove(before, after)
						if err != nil {
							failures <- fmt.Sprintf("writer %d: Remove() error = %v", writer, err)
							return
						}
						if removed.Removed {
							mu.Lock()
							removedSome++
							mu.Unlock()
						}
						continue
					}
					note := fmt.Sprintf("w%d-%d", writer, step)
					added, err := s.Add(before, after, note, testNow.Add(time.Duration(step)*time.Minute))
					if err != nil {
						failures <- fmt.Sprintf("writer %d: Add() error = %v", writer, err)
						return
					}
					mu.Lock()
					written[added.Edge.ID] = added.Edge
					mu.Unlock()
				}
			}()
		}
		ready.Wait()
		close(start)
		done.Wait()
		close(failures)
		for msg := range failures {
			t.Fatalf("seed %d: %s", seed, msg)
		}
		if msg := checkEdges(s, written); msg != "" {
			t.Fatalf("seed %d: %s", seed, msg)
		}
	}
}

// pick は端点の組を引く。cwd は /home の下の参照ごとのディレクトリ。
func pick(r *rand.Rand) (Endpoint, Endpoint) {
	ep := func() Endpoint {
		ref := edgeRefs[r.IntN(len(edgeRefs))]
		return Endpoint{Ref: ref, CWD: "/home/" + strings.NewReplacer(":", "-", "/", "-", "#", "-").Replace(ref)}
	}
	return ep(), ep()
}

// checkEdges は全 writer が止まった後の置き場を見て、破れていればその説明を返す。
func checkEdges(s Store, written map[string]Edge) string {
	entries, err := os.ReadDir(s.Dir)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Sprintf("ReadDir() error = %v", err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") {
			return fmt.Sprintf("書きかけの一時ファイルが残った: %s", entry.Name())
		}
	}
	found, err := s.Find("", "")
	if err != nil {
		return fmt.Sprintf("Find() error = %v", err)
	}
	if len(found) != len(entries) {
		return fmt.Sprintf("ファイル %d 個のうち Find が返したのは %d 本 (壊れた辺がある)", len(entries), len(found))
	}
	pairs := map[[2]string]bool{}
	for _, e := range found {
		pair := [2]string{e.Before.Ref, e.After.Ref}
		switch {
		case pairs[pair]:
			return fmt.Sprintf("参照の組 %v の辺が 2 本ある", pair)
		case e.ID != edgeID(e.Before.Ref, e.After.Ref):
			return fmt.Sprintf("辺 %s の id が参照の組から決まる値 %s でない", e.ID, edgeID(e.Before.Ref, e.After.Ref))
		case e.Before.Ref == e.After.Ref:
			return fmt.Sprintf("自分自身への辺 %s がある", e.ID)
		case !strings.HasPrefix(e.Note, "w"):
			return fmt.Sprintf("辺 %s の note %q は誰も書いていない", e.ID, e.Note)
		}
		pairs[pair] = true
		if _, ok := written[e.ID]; !ok {
			return fmt.Sprintf("辺 %s (%v) は誰も Add していない", e.ID, pair)
		}
		if msg := checkFind(s, e); msg != "" {
			return msg
		}
	}
	return ""
}

// checkFind は辺 e が、その端点の repo と参照で絞った Find に出て、無関係な参照で絞ると出ないことを見る。
func checkFind(s Store, e Edge) string {
	for _, c := range []struct{ repo, ref string }{{e.Before.Repo, ""}, {e.After.Repo, ""}, {"", e.Before.Ref}, {"", e.After.Ref}, {e.Before.Repo, e.After.Ref}} {
		got, err := s.Find(c.repo, c.ref)
		if err != nil {
			return fmt.Sprintf("Find(%q, %q) error = %v", c.repo, c.ref, err)
		}
		if !containsID(got, e.ID) {
			return fmt.Sprintf("Find(%q, %q) に辺 %s が出ない", c.repo, c.ref, e.ID)
		}
	}
	for _, ref := range edgeRefs {
		if ref == e.Before.Ref || ref == e.After.Ref {
			continue
		}
		got, err := s.Find("", ref)
		if err != nil {
			return fmt.Sprintf("Find(%q) error = %v", ref, err)
		}
		if containsID(got, e.ID) {
			return fmt.Sprintf("端点でない参照 %s で絞った Find に辺 %s が出た", ref, e.ID)
		}
	}
	return ""
}

func containsID(edges []Edge, id string) bool {
	for _, e := range edges {
		if e.ID == id {
			return true
		}
	}
	return false
}
