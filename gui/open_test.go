package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func recordingDaemon(err error) (*Daemon, *[]string) {
	opened := &[]string{}
	return &Daemon{open: func(target string) error {
		*opened = append(*opened, target)
		return err
	}}, opened
}

func TestOpenPassesURLsAndExistingPaths(t *testing.T) {
	file := filepath.Join(t.TempDir(), "a b.go")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"https://example.com/a?b=1#c", "http://example.com/", file, filepath.Dir(file)} {
		d, opened := recordingDaemon(nil)
		if err := d.Open(target); err != nil {
			t.Errorf("%s: %v", target, err)
		}
		if len(*opened) != 1 || (*opened)[0] != target {
			t.Errorf("%s: opened = %v", target, *opened)
		}
	}
}

// open はスキームや拡張子でアプリを起こすので、本文 (セッションが書いた文字列) から渡せるものをここで絞る。
func TestOpenRefusesOtherTargets(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	for _, target := range []string{"", "file:///etc/hosts", "javascript:alert(1)", "x-apple.systempreferences:", "https://", "relative/path.go", "-a", missing} {
		d, opened := recordingDaemon(nil)
		if err := d.Open(target); err == nil {
			t.Errorf("%q: err が nil", target)
		}
		if len(*opened) != 0 {
			t.Errorf("%q: 開いてしまった %v", target, *opened)
		}
	}
}

func TestOpenReportsOpenerFailure(t *testing.T) {
	d, _ := recordingDaemon(errors.New("no application"))
	err := d.Open("https://example.com/")
	if err == nil || !strings.Contains(err.Error(), "no application") {
		t.Fatalf("err = %v", err)
	}
}
