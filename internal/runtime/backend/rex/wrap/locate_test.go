package wrap

import (
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

// searchPath は dirs を PATH の形に繋ぐ。fake の rex (sh script) が使うコマンドが引けるよう /usr/bin と /bin を後ろに足す。
func searchPath(dirs ...string) string {
	return strings.Join(append(dirs, "/usr/bin", "/bin"), string(os.PathListSeparator))
}

// writeRex は bin に、受けた引数を log へ 1 行ずつ追記して exit 0 する fake の rex を書く。
// log は bin の置き場とは別のディレクトリに置くので、build のディレクトリを rename しても追記先は変わらない。
func writeRex(t *testing.T, bin, log string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\necho \"$*\" >> '" + log + "'\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

// rexInVersions は build のディレクトリに fake の rex を置き、そのパスを返す。
func rexInVersions(t *testing.T, root, build string) string {
	t.Helper()
	bin := filepath.Join(versionsDir(root, build), "rex")
	writeRex(t, bin, filepath.Join(root, "calls.log"))
	return bin
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
		got, err := Locate()
		if err != nil || got != "/custom/rex" {
			t.Fatalf("Locate() = %q, %v; want /custom/rex", got, err)
		}
	})

	t.Run("PATH にあれば絶対パスで返す", func(t *testing.T) {
		t.Setenv("REX_BIN_PATH", "")
		dir := t.TempDir()
		bin := filepath.Join(dir, "rex")
		writeRex(t, bin, filepath.Join(dir, "calls.log"))
		t.Setenv("PATH", dir)
		got, err := Locate()
		if err != nil || got != bin {
			t.Fatalf("Locate() = %q, %v; want %q", got, err, bin)
		}
	})

	// 引き直しは既定の置き場だけを見る。PATH の項が指す標準外の Versions/ は探さない。
	t.Run("PATH の Versions の項が標準外の置き場で消えていれば引かない", func(t *testing.T) {
		t.Setenv("REX_BIN_PATH", "")
		isolateHome(t)
		root := t.TempDir()
		rexInVersions(t, root, "1203")
		t.Setenv("PATH", searchPath(versionsDir(root, "100")))
		_, err := Locate()
		if !errors.Is(err, exec.ErrNotFound) {
			t.Fatalf("Locate() error = %v, want exec.ErrNotFound", err)
		}
	})

	// Finder や Dock から起きた依存図のアプリ (launchd の最小の PATH) の子でも、rex の既定の置き場から引ける。
	t.Run("PATH に rex が無ければ既定の置き場の Current を引く", func(t *testing.T) {
		t.Setenv("REX_BIN_PATH", "")
		root := installedVersions(isolateHome(t))
		rexInVersions(t, filepath.Dir(root), "1973")
		want := rexInVersions(t, filepath.Dir(root), "Current")
		t.Setenv("PATH", launchdPath)
		got, err := Locate()
		if err != nil || got != want {
			t.Fatalf("Locate() = %q, %v; want %q", got, err, want)
		}
	})

	t.Run("既定の置き場に Current が無ければ一番新しい build を引く", func(t *testing.T) {
		t.Setenv("REX_BIN_PATH", "")
		root := filepath.Dir(installedVersions(isolateHome(t)))
		// 999 と 1203 は rex を持ち、1300 はダウンロード途中でまだ無い。辞書順なら 999 が勝つが数値順で 1203。
		// .staging-… (ダウンロードの途中経過) は数字名でないので候補にしない。
		rexInVersions(t, root, "999")
		want := rexInVersions(t, root, "1203")
		for _, dir := range []string{versionsDir(root, "1300"), versionsDir(root, ".staging-659F4200")} {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		t.Setenv("PATH", launchdPath)
		got, err := Locate()
		if err != nil || got != want {
			t.Fatalf("Locate() = %q, %v; want %q", got, err, want)
		}
	})

	t.Run("どこにも無ければ ErrNotFound", func(t *testing.T) {
		t.Setenv("REX_BIN_PATH", "")
		isolateHome(t)
		t.Setenv("PATH", t.TempDir())
		_, err := Locate()
		if !errors.Is(err, exec.ErrNotFound) {
			t.Fatalf("Locate() error = %v, want exec.ErrNotFound", err)
		}
	})
}
