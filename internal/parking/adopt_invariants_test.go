package parking_test

// Adopt の不変条件 (models/handoff の Handoff.tla の LiveTaskKept と AdoptLeavesOne) を、実物の Store と
// daemonstate.SessionExited (daemon の pid と status.json を読む) を goroutine で交錯させて確かめる。
// 外部テストパッケージにして、mod と同じく parking の公開 API だけで Adopt を押す。
// 世界 (セッションの終了、daemon の tick と停止と入れ替わり、タスクの記録の更新) は 1 本の goroutine が乱択の順で進め、
// 依存図を開いた mod が複数本、好きなときに Adopt を押す。時刻は step ごとに進む作り物で、壁時計には頼らない。
// 反例が出たら seed を出す。反例が出たらコードを直すか挙動を正当化する。性質を弱めて通さない。

import (
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/usadamasa/agents-daemon/internal/apppath"
	"github.com/usadamasa/agents-daemon/internal/daemonstate"
	"github.com/usadamasa/agents-daemon/internal/parking"
	"github.com/usadamasa/agents-daemon/internal/statefile"
	"github.com/usadamasa/agents-daemon/internal/tasklog"
	"github.com/usadamasa/agents-daemon/internal/tasklog/record"
)

const (
	adoptTraces = 150
	adoptMods   = 2
	adoptPushes = 3 // 1 つの mod が To parking を押す回数
	deadSession = "dead"
	deadTask    = "1"
)

// worldAction は世界の 1 手 (Handoff.tla の SessionExit / DaemonTick / DaemonStop と、記録の更新)。
type worldAction int

const (
	actExit    worldAction = iota // セッションが終了する
	actTick                       // 今の daemon が status.json を書く (pane はセッションが生きているときだけ)
	actStop                       // daemon が止まる (pid ファイルが消える)
	actStart                      // daemon が起き直す (pid ファイルだけ。status.json はまだ前のまま)
	actReplace                    // 別の daemon が残した status.json (pid が違う、pane は無い)
	actTouch                      // 生きているセッションがタスクの記録を更新する (updated_at が進む)
	worldActions
)

var worldActionNames = map[worldAction]string{
	actExit: "exit", actTick: "tick", actStop: "stop", actStart: "start", actReplace: "replace", actTouch: "touch",
}

// adoptWorld はテストが持つ世界の状態。mu は世界の手と、mod が Adopt の判定の直後に live を見るのを直列にする。
type adoptWorld struct {
	mu    sync.Mutex
	paths apppath.Paths
	tasks tasklog.Store
	cwd   string
	live  bool
	step  int
	t0    time.Time
}

// tick は作り物の時刻を 1 step 進めて返す。
func (w *adoptWorld) tick() time.Time {
	w.step++
	return w.t0.Add(time.Duration(w.step) * time.Second)
}

func (w *adoptWorld) writePID(pid int) error {
	return statefile.WriteFileAtomic(w.paths.PIDFile(), ".daemon.pid.*", []byte(fmt.Sprintf("%d\n", pid)))
}

// writeStatus は status.json を書く。withPane なら終了していないセッションの pane を載せる。
func (w *adoptWorld) writeStatus(pid int, at time.Time, withPane bool) error {
	snap := daemonstate.StatusSnapshot{PID: pid, UpdatedAt: at, Panes: []daemonstate.PaneStatus{}}
	if withPane {
		snap.Panes = append(snap.Panes, daemonstate.PaneStatus{PaneID: "block:b1", SessionID: deadSession, CWD: w.cwd})
	}
	return statefile.WriteJSONAtomic(w.paths.StatusFile(), ".status.json.*", snap)
}

func (w *adoptWorld) writeRecord(at time.Time) error {
	dir := filepath.Join(w.tasks.Dir, deadSession)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	t := record.Task{ID: deadTask, Subject: "README を直す", Status: "in_progress", SessionID: deadSession, CWD: w.cwd, UpdatedAt: at}
	return statefile.WriteJSONAtomic(filepath.Join(dir, deadTask+".json"), ".task.*", t)
}

// apply は世界の 1 手を進める。
func (w *adoptWorld) apply(a worldAction) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	switch a {
	case actExit:
		w.live = false
		return nil
	case actTick:
		return w.writeStatus(os.Getpid(), w.tick(), w.live)
	case actStop:
		err := os.Remove(w.paths.PIDFile())
		if os.IsNotExist(err) {
			return nil
		}
		return err
	case actStart:
		return w.writePID(os.Getpid())
	case actReplace:
		return w.writeStatus(1, w.tick(), false)
	case actTouch:
		if !w.live {
			return nil
		}
		return w.writeRecord(w.tick())
	}
	return fmt.Errorf("unknown action %d", a)
}

// adoptReached は乱択が通った経路。Adopt が通った回数と、拒んだ理由ごとの回数。
type adoptReached struct {
	mu      sync.Mutex
	adopted int
	refused map[string]int
}

// refusals は daemonstate.SessionExited と parking.Adopt が返す理由の切れ端。全部を一度は通る。
var refusals = []string{"daemon が動いていない", "まだ status.json を書いていない", "タスクの記録より古い", "で生きている", "が無い"}

func (c *adoptReached) refuse(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, key := range refusals {
		if strings.Contains(err.Error(), key) {
			c.refused[key]++
			return
		}
	}
	c.refused["other: "+err.Error()]++
}

func TestAdopt_不変条件を乱択の交錯で破らない(t *testing.T) {
	reached := &adoptReached{refused: map[string]int{}}
	defer func() {
		if reached.adopted == 0 {
			t.Errorf("Adopt が一度も通らなかった (入力の生成を見直す)")
		}
		for _, key := range refusals {
			if reached.refused[key] == 0 {
				t.Errorf("「%s」で拒む経路を一度も通らなかった (入力の生成を見直す): %v", key, reached.refused)
			}
		}
		for key := range reached.refused {
			if strings.HasPrefix(key, "other: ") {
				t.Errorf("知らない理由で拒んだ: %s", key)
			}
		}
	}()
	for seed := range adoptTraces {
		r := rand.New(rand.NewPCG(uint64(seed), 3)) // #nosec G115 -- seed は非負
		w := newAdoptWorld(t, r)
		store := parking.New(w.paths.StateDir())
		actions := make([]worldAction, 4+r.IntN(8))
		for i := range actions {
			actions[i] = worldAction(r.IntN(int(worldActions)))
		}
		failures := make(chan string, adoptMods*adoptPushes+len(actions))
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			for _, a := range actions {
				if err := w.apply(a); err != nil {
					failures <- fmt.Sprintf("world %s: %v", worldActionNames[a], err)
					return
				}
				runtime.Gosched()
			}
		}()
		for mod := range adoptMods {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for range adoptPushes {
					runtime.Gosched()
					var verdict error
					exited := func(sessionID string, after time.Time) error {
						verdict = w.exited(failures, mod, sessionID, after)
						return verdict
					}
					_, err := store.Adopt(deadSession, deadTask, w.tasks, exited, w.t0)
					if err != nil {
						reached.refuse(err)
						continue
					}
					if verdict != nil {
						failures <- fmt.Sprintf("mod %d: 終了したと確かめられない (%v) のにタスクの記録を消した", mod, verdict)
						return
					}
					// 記録を消した直後にセッションがまだ生きていれば、消した時点でも生きていた (終了したら戻らない)。
					w.mu.Lock()
					live := w.live
					w.mu.Unlock()
					if live {
						failures <- fmt.Sprintf("mod %d: LiveTaskKept: 生きているセッション %s のタスクの記録を消した", mod, deadSession)
						return
					}
					reached.mu.Lock()
					reached.adopted++
					reached.mu.Unlock()
				}
			}()
		}
		wg.Wait()
		close(failures)
		for msg := range failures {
			t.Fatalf("seed %d (actions %v): %s", seed, names(actions), msg)
		}
		if msg := checkAdoptLeavesOne(w, store); msg != "" {
			t.Fatalf("seed %d (actions %v): %s", seed, names(actions), msg)
		}
	}
}

// newAdoptWorld は生きているセッションのタスクの記録 1 つと、動いている daemon (このテストプロセス) を置く。
// 半分の seed では、daemon がそのセッションを見る前 (記録より古い) に書いた pane の無い status.json から始める。
// Adopt はこれを「終了した」と読んではいけない。
func newAdoptWorld(t *testing.T, r *rand.Rand) *adoptWorld {
	t.Helper()
	root := t.TempDir()
	paths := apppath.New(root, filepath.Join(root, "state"), filepath.Join(root, "config"))
	w := &adoptWorld{paths: paths, tasks: tasklog.New(paths.StateDir()), cwd: filepath.Join(root, "repo"), live: true, t0: time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)}
	if err := os.MkdirAll(filepath.Join(paths.StateDir()), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(w.cwd, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := w.writePID(os.Getpid()); err != nil {
		t.Fatal(err)
	}
	staleFirst := r.IntN(2) == 0
	if staleFirst {
		if err := w.writeStatus(os.Getpid(), w.tick(), false); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.writeRecord(w.tick()); err != nil {
		t.Fatal(err)
	}
	if !staleFirst {
		if err := w.writeStatus(os.Getpid(), w.tick(), true); err != nil {
			t.Fatal(err)
		}
	}
	return w
}

// exited は Adopt に渡す「終了したと確かめる」の中身。実物の daemonstate.SessionExited を呼び、通した直後にセッションがまだ生きていれば
// LiveTaskKept の破れとして記録する。セッションは終了したら戻らないので、通した後に生きているなら判定の時点でも生きていた。
func (w *adoptWorld) exited(failures chan<- string, mod int, sessionID string, after time.Time) error {
	err := daemonstate.SessionExited(w.paths, sessionID, after)
	if err != nil {
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.live {
		failures <- fmt.Sprintf("mod %d: LiveTaskKept: 生きているセッション %s を終了したと判定した (記録 %s)", mod, sessionID, after.Format(time.RFC3339))
	}
	return nil
}

// checkAdoptLeavesOne は全部が止まった後、タスクの記録かメモの少なくとも一方が残っていることを見る。
// Adopt が一度も通っていなければ、記録が残りメモは無い。
func checkAdoptLeavesOne(w *adoptWorld, store parking.Store) string {
	_, recorded := w.tasks.Get(deadSession, deadTask)
	memos, err := store.Find("", "")
	if err != nil {
		return fmt.Sprintf("Find() error = %v", err)
	}
	switch {
	case !recorded && len(memos) == 0:
		return "AdoptLeavesOne: タスクの記録もメモも無い"
	case len(memos) > 1:
		return fmt.Sprintf("同じタスクからメモが %d つできた", len(memos))
	case len(memos) == 1 && (memos[0].Kind != parking.KindScope || memos[0].Origin != parking.OriginAdopted || memos[0].Title != "README を直す"):
		return fmt.Sprintf("メモの中身が違う: %+v", memos[0])
	case len(memos) == 1 && (memos[0].Status != parking.StatusOpen || memos[0].HandedTo != ""):
		return fmt.Sprintf("adopt したメモが open で宛先無しでない: %+v", memos[0])
	}
	return ""
}

func names(actions []worldAction) []string {
	out := make([]string, len(actions))
	for i, a := range actions {
		out[i] = worldActionNames[a]
	}
	return out
}
