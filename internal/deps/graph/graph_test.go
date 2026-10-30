package graph

import "testing"

func TestKeyLess(t *testing.T) {
	if !KeyLess("task:A/9", "task:A/10") || KeyLess("task:A/10", "task:A/9") || !KeyLess("task:A/1", "task:B/0") ||
		!KeyLess("todo:A/2", "todo:A/10") || !KeyLess("sid:A", "task:A/1") || !KeyLess("task:A/1", "todo:A/0") {
		t.Errorf("KeyLess の並びが合わない")
	}
}

// ノードの状態は作業の進み、セッションの生死、セッションの活動だけで決まる。完了は生死より先、終了は未着手より先に見る。
func TestNodeState(t *testing.T) {
	activities := []string{StateWorking, StateDone, StateQuestion, StatePermission, StateError, StateLimit, StateUnknown}
	for _, act := range activities {
		for _, c := range []struct {
			progress string
			live     bool
			want     string
		}{
			{"completed", true, StateCompleted},
			{"completed", false, StateCompleted},
			{"pending", false, StateExited},
			{"in_progress", false, StateExited},
			{"pending", true, StatePending},
			{"in_progress", true, act},
		} {
			// 生きていないセッションには活動が無い。
			a := act
			if !c.live {
				a = ""
			}
			if got := nodeState(c.progress, c.live, a); got != c.want {
				t.Errorf("nodeState(%q, %v, %q) = %q, want %q", c.progress, c.live, a, got, c.want)
			}
		}
	}
}

// 止まっているかは 2 つ: halted はセッション自身が人の手か時間を待っているか失敗した状態、stuck はそれに
// 完了していない待ちの輪を足したもの。
func TestStuck(t *testing.T) {
	for _, c := range []struct {
		state    string
		deadlock bool
		halted   bool
		stuck    bool
	}{
		{StateQuestion, false, true, true},
		{StatePermission, false, true, true},
		{StateLimit, false, true, true},
		{StateError, false, true, true},
		{StateWorking, false, false, false},
		{StateDone, false, false, false},
		{StateUnknown, false, false, false},
		{StatePending, false, false, false},
		{StateExited, false, false, false},
		{StateCompleted, false, false, false},
		{StatePending, true, false, true},
		{StateWorking, true, false, true},
		{StateQuestion, true, true, true},
		{StateCompleted, true, false, false},
	} {
		n := Node{State: c.state, Deadlock: c.deadlock}
		markStuck(&n)
		if n.Halted != c.halted || n.Stuck != c.stuck {
			t.Errorf("%s deadlock=%v: halted/stuck = %v/%v, want %v/%v", c.state, c.deadlock, n.Halted, n.Stuck, c.halted, c.stuck)
		}
	}
}
