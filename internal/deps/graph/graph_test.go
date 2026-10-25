package graph

import "testing"

func TestKeyLess(t *testing.T) {
	if !KeyLess("task:A/9", "task:A/10") || KeyLess("task:A/10", "task:A/9") || !KeyLess("task:A/1", "task:B/0") ||
		!KeyLess("todo:A/2", "todo:A/10") || !KeyLess("sid:A", "task:A/1") || !KeyLess("task:A/1", "todo:A/0") {
		t.Errorf("KeyLess の並びが合わない")
	}
}
