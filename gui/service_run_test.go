package main

import (
	"errors"
	"strings"
	"testing"
)

func TestRunRejectsCommandsOutsideTheAllowList(t *testing.T) {
	d := &Daemon{bin: fakeBin(t)}
	for _, command := range []string{"deps", "park list", "daemon stop", "", "park set; rm -rf /"} {
		if _, err := d.Run(command, "{}"); err == nil {
			t.Errorf("%q が通った", command)
		}
	}
}

func TestRunPassesSubcommandWordsAndStdin(t *testing.T) {
	d := &Daemon{bin: fakeBin(t)}
	out, err := d.Run("park set", `{"id":"m1","status":"discarded"}`)
	if err != nil {
		t.Fatal(err)
	}
	if out != `args=park set {"id":"m1","status":"discarded"}` {
		t.Fatalf("stdout: %q", out)
	}
}

// raisingDaemon は Rex.app を手前に出す呼び出しを数える Daemon。
func raisingDaemon(t *testing.T, err error) (*Daemon, *int) {
	t.Helper()
	raised := new(int)
	return &Daemon{bin: fakeBin(t), raise: func() error {
		*raised++
		return err
	}}, raised
}

// focus は Rex.app の表示を切り替えるだけなので、通った後にアプリが Rex.app を手前に出す。
func TestRunFocusRaisesRexAfterSuccess(t *testing.T) {
	d, raised := raisingDaemon(t, nil)
	if _, err := d.Run("focus", `{"to_session":"s1"}`); err != nil {
		t.Fatal(err)
	}
	if *raised != 1 {
		t.Fatalf("手前に出した回数 = %d, want 1", *raised)
	}
}

func TestRunFocusDoesNotRaiseRexOnFailure(t *testing.T) {
	t.Setenv("FAKE_EXIT", "1")
	d, raised := raisingDaemon(t, nil)
	if _, err := d.Run("focus", `{"to_session":"s1"}`); err == nil {
		t.Fatal("err が nil")
	}
	if *raised != 0 {
		t.Fatalf("表示を切り替えられなかったのに手前に出した (%d 回)", *raised)
	}
}

func TestRunFocusReportsRaiseFailure(t *testing.T) {
	d, _ := raisingDaemon(t, errors.New("no application"))
	_, err := d.Run("focus", `{"to_session":"s1"}`)
	if err == nil || !strings.Contains(err.Error(), "no application") {
		t.Fatalf("err = %v", err)
	}
}

func TestRunOtherCommandsDoNotRaiseRex(t *testing.T) {
	d, raised := raisingDaemon(t, nil)
	if _, err := d.Run("tell", `{"to_session":"s1","text":"x"}`); err != nil {
		t.Fatal(err)
	}
	if *raised != 0 {
		t.Fatalf("tell で手前に出した (%d 回)", *raised)
	}
}

func TestRunReportsStderrOnFailure(t *testing.T) {
	t.Setenv("FAKE_EXIT", "1")
	d := &Daemon{bin: fakeBin(t)}
	_, err := d.Run("edge add", "{}")
	if err == nil || !strings.Contains(err.Error(), "broken") {
		t.Fatalf("stderr が err に無い: %v", err)
	}
}
