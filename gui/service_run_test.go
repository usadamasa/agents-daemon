package main

import (
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

func TestRunReportsStderrOnFailure(t *testing.T) {
	t.Setenv("FAKE_EXIT", "1")
	d := &Daemon{bin: fakeBin(t)}
	_, err := d.Run("edge add", "{}")
	if err == nil || !strings.Contains(err.Error(), "broken") {
		t.Fatalf("stderr が err に無い: %v", err)
	}
}
