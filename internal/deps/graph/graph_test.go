package graph

import "testing"

func TestKeyLess(t *testing.T) {
	if !KeyLess("A/9", "A/10") || KeyLess("A/10", "A/9") || !KeyLess("A/1", "B/0") || !KeyLess("A/session", "A/todo-0") {
		t.Errorf("KeyLess の並びが合わない")
	}
}
