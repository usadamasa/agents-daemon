package tasklog

// タスクの記録の不変条件 (models/handoff の TaskLog.tla の RecordsComplete) を、実物の Store へ hook の入力を goroutine で
// 交錯させて確かめる。model の側 (Claude Code) は TaskCreate と TaskList を順に呼び、その hook の入力は順序の決まらない形で
// 届く (同じターンの並列の tool)。seed が固定するのは model の呼び方と hook の並べ替えで、交錯はスケジューラが決める。
// 反例が出たら seed と呼び方を出す。反例が出たらコードを直すか挙動を正当化する。性質を弱めて通さない。

import (
	"fmt"
	"math/rand/v2"
	"strconv"
	"sync"
	"testing"

	"github.com/usadamasa/agents-daemon/internal/tasklog/record"
)

const (
	taskLogTraces   = 150
	taskLogMaxTasks = 5 // TaskLog.cfg の MaxTasks より広く取る
)

func TestIngest_TaskCreateの記録は乱択の交錯で失われない(t *testing.T) {
	base := fixture(t, "task-create-alpha")
	// 一覧の後に作られた記録を、後から動いた TaskList の hook が消しに行く形を一度は通る (通らなければ sync の保護を確かめていない)。
	listsBeforeLater := 0
	defer func() {
		if listsBeforeLater == 0 {
			t.Errorf("TaskList の後に TaskCreate が続く呼び方を一度も引かなかった (入力の生成を見直す)")
		}
	}()
	for seed := range taskLogTraces {
		r := rand.New(rand.NewPCG(uint64(seed), 4)) // #nosec G115 -- seed は非負
		s := New(t.TempDir())
		var created []string
		var calls []string
		var hooks [][]byte
		for range 2 + r.IntN(8) {
			if r.IntN(2) == 0 && len(created) < taskLogMaxTasks {
				id := strconv.Itoa(len(created) + 1)
				created = append(created, id)
				calls = append(calls, "create "+id)
				hooks = append(hooks, withTool(t, base, "TaskCreate", map[string]any{"subject": "task " + id}, map[string]any{"task": map[string]any{"id": id, "subject": "task " + id}}))
				if len(calls) > 1 && calls[len(calls)-2][:4] == "list" {
					listsBeforeLater++
				}
				continue
			}
			listed := make([]map[string]any, 0, len(created))
			for _, id := range created {
				listed = append(listed, map[string]any{"id": id, "subject": "task " + id, "status": "pending", "blockedBy": []string{}})
			}
			calls = append(calls, fmt.Sprintf("list %v", created))
			hooks = append(hooks, withTool(t, base, "TaskList", nil, map[string]any{"tasks": listed}))
		}
		r.Shuffle(len(hooks), func(i, j int) { hooks[i], hooks[j] = hooks[j], hooks[i] })

		var ready, done sync.WaitGroup
		start := make(chan struct{})
		errs := make(chan error, len(hooks))
		for _, input := range hooks {
			ready.Add(1)
			done.Add(1)
			go func() {
				defer done.Done()
				ready.Done()
				<-start
				if _, err := s.Ingest(input, t0); err != nil {
					errs <- err
				}
			}()
		}
		ready.Wait()
		close(start)
		done.Wait()
		close(errs)
		for err := range errs {
			t.Fatalf("seed %d (%v): Ingest() error = %v", seed, calls, err)
		}

		got := byID(t, s)
		for _, id := range created {
			if _, ok := got[id]; !ok {
				t.Fatalf("seed %d (%v): RecordsComplete: TaskCreate の記録 %s が失われた (残り %v)", seed, calls, id, keys(got))
			}
		}
		if len(got) != len(created) {
			t.Fatalf("seed %d (%v): 作っていないタスクの記録がある: %v", seed, calls, keys(got))
		}
	}
}

func keys(m map[string]record.Task) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
