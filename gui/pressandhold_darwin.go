//go:build darwin

package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Foundation
#import <Foundation/Foundation.h>

static void agentTasksDisablePressAndHold(void) {
	[[NSUserDefaults standardUserDefaults] registerDefaults:@{@"ApplePressAndHoldEnabled": @NO}];
}

static int agentTasksPressAndHold(int *enabled) {
	id v = [[NSUserDefaults standardUserDefaults] objectForKey:@"ApplePressAndHoldEnabled"];
	if (v == nil) {
		*enabled = 0;
		return 0;
	}
	*enabled = [v boolValue] ? 1 : 0;
	return 1;
}
*/
import "C"

// disablePressAndHold は英数キーの長押しでアクセント記号の候補を出す macOS の既定を、このアプリでは切る (指示や補足の
// textarea で文字を繰り返せるように)。registerDefaults は保存しない既定値なので、利用者が
// `defaults write com.github.usadamasa.agents-daemon.agent-tasks ApplePressAndHoldEnabled -bool true` で戻せる。
// 文字入力の仕組みが既定値を読む前、アプリを作る前に呼ぶ。
func disablePressAndHold() {
	C.agentTasksDisablePressAndHold()
}

// pressAndHold は ApplePressAndHoldEnabled に値があるかと、その値を返す (テスト用)。
func pressAndHold() (set, enabled bool) {
	var e C.int
	s := C.agentTasksPressAndHold(&e)
	return s != 0, e != 0
}
