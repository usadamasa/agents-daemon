package worklog

// 記録の不変条件 (models/work の Work.tla の HumanSubjectKept、EndedStays、NoLostUpdate) を、実物の Store へ hook の入力と
// `work set` を goroutine で交錯させて確かめる。モデルは lock の中で読んでから自分の欄だけを書く形 (Locked) で holds に
// なるので、実装が同じ形かをここで見る。反例が出たらコードを直すか挙動を正当化する。性質を弱めて通さない。

import (
	"math/rand/v2"
	"sync"
	"testing"
)

const (
	workTraces  = 60
	workPrompts = 6
)

func TestIngest_不変条件を乱択の交錯で破らない(t *testing.T) {
	for seed := range workTraces {
		r := rand.New(rand.NewPCG(uint64(seed), 7)) // #nosec G115 -- seed は非負
		s := New(t.TempDir())
		mustIngest(t, s, hook("UserPromptSubmit", `"prompt": "最初"`), now)

		// 交錯させる書き込み: prompt の hook、Stop の hook、companion の work set、人の work set、SessionEnd。
		type step func() error
		var steps []step
		prompts := 1
		for range 1 + r.IntN(workPrompts) {
			prompts++
			steps = append(steps, func() error { _, err := s.Ingest(hook("UserPromptSubmit", `"prompt": "次"`), now); return err })
			steps = append(steps, func() error { _, err := s.Ingest(hook("Stop", `"last_assistant_message": "済"`), now); return err })
		}
		steps = append(steps, func() error { _, err := s.Set("s1", Change{Inferred: new("推定")}, now); return err })
		steps = append(steps, func() error { _, err := s.Set("s1", Change{Subject: new("人の題")}, now); return err })
		steps = append(steps, func() error { _, err := s.Ingest(hook("SessionEnd", `"reason": "other"`), now); return err })
		r.Shuffle(len(steps), func(i, j int) { steps[i], steps[j] = steps[j], steps[i] })

		var ready, done sync.WaitGroup
		start := make(chan struct{})
		errs := make(chan error, len(steps))
		for _, st := range steps {
			ready.Add(1)
			done.Go(func() {
				ready.Done()
				<-start
				if err := st(); err != nil {
					errs <- err
				}
			})
		}
		ready.Wait()
		close(start)
		done.Wait()
		close(errs)
		for err := range errs {
			t.Fatalf("seed %d: %v", seed, err)
		}

		got := mustGet(t, s, "s1")
		if got.Turns != prompts {
			t.Errorf("seed %d: NoLostUpdate: turns = %d, want %d", seed, got.Turns, prompts)
		}
		if got.Subject != "人の題" || got.Origin != OriginHuman {
			t.Errorf("seed %d: HumanSubjectKept: subject = %q (%s)", seed, got.Subject, got.Origin)
		}
		if !got.Ended() {
			t.Errorf("seed %d: EndedStays: ended が消えた", seed)
		}
		if got.Inferred != "推定" || got.Summary != "済" {
			t.Errorf("seed %d: inferred = %q, summary = %q", seed, got.Inferred, got.Summary)
		}
	}
}
