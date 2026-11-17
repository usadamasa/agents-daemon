package cli

import (
	"context"
	"errors"
	"os"
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

// rex の自動更新で旧 build のディレクトリが消えても、同じ呼び出しが既定の置き場から引き直した rex で通る。
func TestRunner_RelocatesWhenBinaryVanishes(t *testing.T) {
	t.Setenv("REX_BIN_PATH", "")
	root := filepath.Dir(installedVersions(isolateHome(t)))
	rexInVersions(t, root, "100")
	t.Setenv("PATH", searchPath(versionsDir(root, "100")))

	r := FromEnv(0)
	if _, err := r.Run(context.Background(), "session", "kill", "session:a"); err != nil {
		t.Fatalf("更新前の Run() error = %v", err)
	}
	if err := os.Rename(filepath.Join(root, "Versions", "100"), filepath.Join(root, "Versions", "101")); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Run(context.Background(), "session", "kill", "session:a"); err != nil {
		t.Fatalf("更新後の Run() error = %v", err)
	}
	if want := filepath.Join(versionsDir(root, "101"), "rex"); r.BinPath() != want {
		t.Errorf("BinPath() = %q, want %q", r.BinPath(), want)
	}
	b, err := os.ReadFile(filepath.Join(root, "calls.log")) // #nosec G304 -- テストの一時ディレクトリ
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(b), strings.Repeat("--autostart=false session kill session:a\n", 2); got != want {
		t.Errorf("calls = %q, want %q", got, want)
	}
}

// REX_BIN_PATH で固定したときは引き直さず、そのまま失敗を返す。
func TestRunner_PinnedBinPathIsNotRelocated(t *testing.T) {
	root := t.TempDir()
	bin := rexInVersions(t, root, "100")
	rexInVersions(t, root, "101")
	t.Setenv("REX_BIN_PATH", bin)
	t.Setenv("PATH", searchPath(versionsDir(root, "100")))

	r := FromEnv(0)
	if err := os.RemoveAll(filepath.Join(root, "Versions", "100")); err != nil {
		t.Fatal(err)
	}
	_, err := r.Run(context.Background(), "session", "kill", "session:a")
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Run() error = %v, want os.ErrNotExist (引き直さない)", err)
	}
}
