//go:build darwin

package main

import "testing"

func TestDisablePressAndHoldTurnsOffTheDefault(t *testing.T) {
	disablePressAndHold()
	// 値が無いときの boolForKey も偽になるので、値があることも確かめる。
	set, enabled := pressAndHold()
	if !set || enabled {
		t.Fatalf("ApplePressAndHoldEnabled: set=%v enabled=%v (英数の長押しが繰り返さない)", set, enabled)
	}
}
