package rexcli

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// versionsBinDir は rex が block の PATH に載せるディレクトリの形 (2026-10-18 の実測)。
const versionsBinDir = "Rex Server.app/Contents/MacOS"

// versionsDir は root の下に rex の自動更新の置き場 (`Versions/<build>/…`) を真似て返す。
func versionsDir(root, build string) string {
	return filepath.Join(root, "Versions", build, versionsBinDir)
}

// searchPath は dirs を PATH の形に繋ぐ。fake の rex (sh script) が使う sed と cat が引けるよう /usr/bin と /bin を後ろに足す。
func searchPath(dirs ...string) string {
	return strings.Join(append(dirs, "/usr/bin", "/bin"), string(os.PathListSeparator))
}

// fakeRexInVersions は build のディレクトリに fake の rex を置く。fixture は root/fixtures から読むので、
// build のディレクトリを rename しても応答は変わらない。
func fakeRexInVersions(t *testing.T, root, build string) *fakeRex {
	t.Helper()
	return newFakeRexAt(t, filepath.Join(root, "fixtures"), filepath.Join(versionsDir(root, build), "rex"))
}

// launchdPath は Finder や Dock から起きた .app が受け取る PATH。rex の項を持たない。
const launchdPath = "/usr/bin:/bin:/usr/sbin:/sbin"

// installedVersions は HOME の下の rex の既定の置き場の Versions/ を返す。
func installedVersions(home string) string {
	return filepath.Join(home, "Library", "Application Support", "com.superlogical.rex", "RexServer", "Versions")
}

// isolateHome は HOME を空の一時ディレクトリにし、開発機に入っている本物の rex を既定の置き場から引かないようにする。
func isolateHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	return home
}

func TestLocateBinary(t *testing.T) {
	t.Run("REX_BIN_PATH があればそのまま返す", func(t *testing.T) {
		t.Setenv("REX_BIN_PATH", "/custom/rex")
		t.Setenv("PATH", t.TempDir())
		got, err := locateBinary()
		if err != nil || got != "/custom/rex" {
			t.Fatalf("locateBinary() = %q, %v; want /custom/rex", got, err)
		}
	})

	t.Run("PATH にあれば絶対パスで返す", func(t *testing.T) {
		t.Setenv("REX_BIN_PATH", "")
		f := newFakeRex(t)
		t.Setenv("PATH", filepath.Dir(f.bin))
		got, err := locateBinary()
		if err != nil || got != f.bin {
			t.Fatalf("locateBinary() = %q, %v; want %q", got, err, f.bin)
		}
	})

	// 引き直しは既定の置き場だけを見る。PATH の項が指す標準外の Versions/ は探さない。
	t.Run("PATH の Versions の項が標準外の置き場で消えていれば引かない", func(t *testing.T) {
		t.Setenv("REX_BIN_PATH", "")
		isolateHome(t)
		root := t.TempDir()
		fakeRexInVersions(t, root, "1203")
		t.Setenv("PATH", searchPath(versionsDir(root, "100")))
		_, err := locateBinary()
		if !errors.Is(err, exec.ErrNotFound) {
			t.Fatalf("locateBinary() error = %v, want exec.ErrNotFound", err)
		}
	})

	// Finder や Dock から起きた依存図のアプリ (launchd の最小の PATH) の子でも、rex の既定の置き場から引ける。
	t.Run("PATH に rex が無ければ既定の置き場の Current を引く", func(t *testing.T) {
		t.Setenv("REX_BIN_PATH", "")
		root := installedVersions(isolateHome(t))
		fakeRexInVersions(t, filepath.Dir(root), "1973")
		want := fakeRexInVersions(t, filepath.Dir(root), "Current").bin
		t.Setenv("PATH", launchdPath)
		got, err := locateBinary()
		if err != nil || got != want {
			t.Fatalf("locateBinary() = %q, %v; want %q", got, err, want)
		}
	})

	t.Run("既定の置き場に Current が無ければ一番新しい build を引く", func(t *testing.T) {
		t.Setenv("REX_BIN_PATH", "")
		root := filepath.Dir(installedVersions(isolateHome(t)))
		// 999 と 1203 は rex を持ち、1300 はダウンロード途中でまだ無い。辞書順なら 999 が勝つが数値順で 1203。
		// .staging-… (ダウンロードの途中経過) は数字名でないので候補にしない。
		fakeRexInVersions(t, root, "999")
		want := fakeRexInVersions(t, root, "1203").bin
		for _, dir := range []string{versionsDir(root, "1300"), versionsDir(root, ".staging-659F4200")} {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		t.Setenv("PATH", launchdPath)
		got, err := locateBinary()
		if err != nil || got != want {
			t.Fatalf("locateBinary() = %q, %v; want %q", got, err, want)
		}
	})

	t.Run("どこにも無ければ ErrNotFound", func(t *testing.T) {
		t.Setenv("REX_BIN_PATH", "")
		isolateHome(t)
		t.Setenv("PATH", t.TempDir())
		_, err := locateBinary()
		if !errors.Is(err, exec.ErrNotFound) {
			t.Fatalf("locateBinary() error = %v, want exec.ErrNotFound", err)
		}
	})
}

// rex の自動更新で旧 build のディレクトリが消えても、同じ呼び出しが既定の置き場から引き直した rex で通る。
func TestExecClient_RelocatesWhenBinaryVanishes(t *testing.T) {
	t.Setenv("REX_BIN_PATH", "")
	root := filepath.Dir(installedVersions(isolateHome(t)))
	f := fakeRexInVersions(t, root, "100")
	t.Setenv("PATH", searchPath(versionsDir(root, "100")))

	c := newExecClientWithTimeout(staticResolver(nil), noCommandTimeout)
	if err := c.SessionKill(context.Background(), sessionA); err != nil {
		t.Fatalf("更新前の SessionKill() error = %v", err)
	}
	if err := os.Rename(filepath.Join(root, "Versions", "100"), filepath.Join(root, "Versions", "101")); err != nil {
		t.Fatal(err)
	}
	if err := c.SessionKill(context.Background(), sessionA); err != nil {
		t.Fatalf("更新後の SessionKill() error = %v", err)
	}
	if want := filepath.Join(versionsDir(root, "101"), "rex"); c.binPath != want {
		t.Errorf("binPath = %q, want %q", c.binPath, want)
	}
	f.assertCalls(t,
		"--autostart=false session kill "+sessionA,
		"--autostart=false session kill "+sessionA,
	)
}

// REX_BIN_PATH で固定したときは引き直さず、そのまま失敗を返す。
func TestExecClient_PinnedBinPathIsNotRelocated(t *testing.T) {
	root := t.TempDir()
	f := fakeRexInVersions(t, root, "100")
	fakeRexInVersions(t, root, "101")
	t.Setenv("REX_BIN_PATH", f.bin)
	t.Setenv("PATH", searchPath(versionsDir(root, "100")))

	c := newExecClientWithTimeout(staticResolver(nil), noCommandTimeout)
	if err := os.RemoveAll(filepath.Join(root, "Versions", "100")); err != nil {
		t.Fatal(err)
	}
	err := c.SessionKill(context.Background(), sessionA)
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("SessionKill() error = %v, want os.ErrNotExist (引き直さない)", err)
	}
}
