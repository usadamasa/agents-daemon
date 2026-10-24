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

	t.Run("PATH の Versions の項が消えていれば一番新しい build を引く", func(t *testing.T) {
		t.Setenv("REX_BIN_PATH", "")
		root := t.TempDir()
		// 999 と 1203 は rex を持ち、1300 はダウンロード途中でまだ無い。辞書順なら 999 が勝つが数値順で 1203。
		// Current (build の複製) と .staging-… (ダウンロードの途中経過) は数字名でないので候補にしない。
		fakeRexInVersions(t, root, "999")
		want := fakeRexInVersions(t, root, "1203").bin
		fakeRexInVersions(t, root, "Current")
		for _, dir := range []string{versionsDir(root, "1300"), versionsDir(root, ".staging-659F4200")} {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		t.Setenv("PATH", searchPath(t.TempDir(), versionsDir(root, "100")))
		got, err := locateBinary()
		if err != nil || got != want {
			t.Fatalf("locateBinary() = %q, %v; want %q", got, err, want)
		}
	})

	t.Run("どこにも無ければ ErrNotFound", func(t *testing.T) {
		t.Setenv("REX_BIN_PATH", "")
		t.Setenv("PATH", t.TempDir())
		_, err := locateBinary()
		if !errors.Is(err, exec.ErrNotFound) {
			t.Fatalf("locateBinary() error = %v, want exec.ErrNotFound", err)
		}
	})
}

// rex の自動更新で旧 build のディレクトリが消えても、同じ呼び出しが引き直した rex で通る。
func TestExecClient_RelocatesWhenBinaryVanishes(t *testing.T) {
	t.Setenv("REX_BIN_PATH", "")
	root := t.TempDir()
	f := fakeRexInVersions(t, root, "100")
	f.put(t, "server-status.json", measured(t, measuredOct10, "server_status.json"))
	t.Setenv("PATH", searchPath(versionsDir(root, "100")))

	c := newExecClientWithTimeout(staticResolver(nil), noCommandTimeout)
	if err := c.Reachable(context.Background()); err != nil {
		t.Fatalf("更新前の Reachable() error = %v", err)
	}
	if err := os.Rename(filepath.Join(root, "Versions", "100"), filepath.Join(root, "Versions", "101")); err != nil {
		t.Fatal(err)
	}
	if err := c.Reachable(context.Background()); err != nil {
		t.Fatalf("更新後の Reachable() error = %v", err)
	}
	if want := filepath.Join(versionsDir(root, "101"), "rex"); c.binPath != want {
		t.Errorf("binPath = %q, want %q", c.binPath, want)
	}
	f.assertCalls(t,
		"--autostart=false api call server.status",
		"--autostart=false api call server.status",
	)
}

// REX_BIN_PATH で固定したときは引き直さず、そのまま失敗を返す。
func TestExecClient_PinnedBinPathIsNotRelocated(t *testing.T) {
	root := t.TempDir()
	f := fakeRexInVersions(t, root, "100")
	f.put(t, "server-status.json", measured(t, measuredOct10, "server_status.json"))
	fakeRexInVersions(t, root, "101")
	t.Setenv("REX_BIN_PATH", f.bin)
	t.Setenv("PATH", searchPath(versionsDir(root, "100")))

	c := newExecClientWithTimeout(staticResolver(nil), noCommandTimeout)
	if err := os.RemoveAll(filepath.Join(root, "Versions", "100")); err != nil {
		t.Fatal(err)
	}
	err := c.Reachable(context.Background())
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Reachable() error = %v, want os.ErrNotExist (引き直さない)", err)
	}
}
