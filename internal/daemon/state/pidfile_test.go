package state

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// deadPID は「確実に生存していない」PID を用意する。ハードコードした数値を
// 期待するのではなく、実際にプロセスを起動して終了を待つことで、テスト実行時の
// PID 空間の状態に依存せず確実に dead な PID を得る。
func deadPID(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("true")
	if err := cmd.Start(); err != nil {
		t.Fatalf("プロセスの起動に失敗: %v", err)
	}
	pid := cmd.Process.Pid
	if err := cmd.Wait(); err != nil {
		t.Fatalf("プロセスの終了待ちに失敗: %v", err)
	}
	return pid
}

func TestPidAlive(t *testing.T) {
	t.Run("自分自身のPIDは生存している", func(t *testing.T) {
		if !PIDAlive(os.Getpid()) {
			t.Error("PIDAlive(os.Getpid()) = false, want true")
		}
	})

	t.Run("終了したプロセスのPIDは生存していない", func(t *testing.T) {
		pid := deadPID(t)
		if PIDAlive(pid) {
			t.Errorf("PIDAlive(%d) = true, want false (プロセスは既に終了している)", pid)
		}
	})

	t.Run("0以下のPIDは生存していない扱い", func(t *testing.T) {
		if PIDAlive(0) {
			t.Error("PIDAlive(0) = true, want false")
		}
		if PIDAlive(-1) {
			t.Error("PIDAlive(-1) = true, want false")
		}
	})
}

func TestWriteReadPIDFile(t *testing.T) {
	t.Run("書き込んだPIDをそのまま読み戻せる", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "nested", "daemon.pid")
		if err := WritePIDFile(path, 12345); err != nil {
			t.Fatalf("WritePIDFile() error = %v", err)
		}
		pid, err := ReadPIDFile(path)
		if err != nil {
			t.Fatalf("ReadPIDFile() error = %v", err)
		}
		if pid != 12345 {
			t.Errorf("ReadPIDFile() = %d, want 12345", pid)
		}
	})

	t.Run("ファイルが存在しない場合は0とエラー無しを返す", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "no-such-file")
		pid, err := ReadPIDFile(path)
		if err != nil {
			t.Fatalf("ReadPIDFile() error = %v, want nil", err)
		}
		if pid != 0 {
			t.Errorf("ReadPIDFile() = %d, want 0", pid)
		}
	})

	t.Run("中身が数値でない場合は0とエラー無しを返す", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "daemon.pid")
		if err := os.WriteFile(path, []byte("not-a-pid\n"), 0o644); err != nil {
			t.Fatalf("セットアップに失敗: %v", err)
		}
		pid, err := ReadPIDFile(path)
		if err != nil {
			t.Fatalf("ReadPIDFile() error = %v, want nil", err)
		}
		if pid != 0 {
			t.Errorf("ReadPIDFile() = %d, want 0", pid)
		}
	})

	t.Run("空ファイルの場合は0とエラー無しを返す", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "daemon.pid")
		if err := os.WriteFile(path, []byte(""), 0o644); err != nil {
			t.Fatalf("セットアップに失敗: %v", err)
		}
		pid, err := ReadPIDFile(path)
		if err != nil {
			t.Fatalf("ReadPIDFile() error = %v, want nil", err)
		}
		if pid != 0 {
			t.Errorf("ReadPIDFile() = %d, want 0", pid)
		}
	})
}

func TestRemovePIDFile(t *testing.T) {
	t.Run("存在するファイルを削除する", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "daemon.pid")
		if err := WritePIDFile(path, 1); err != nil {
			t.Fatalf("セットアップに失敗: %v", err)
		}
		if err := removePIDFile(path); err != nil {
			t.Fatalf("RemovePIDFile() error = %v", err)
		}
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("ファイルが削除されていない: err = %v", err)
		}
	})

	t.Run("存在しないファイルはエラーにしない", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "no-such-file")
		if err := removePIDFile(path); err != nil {
			t.Errorf("RemovePIDFile() error = %v, want nil", err)
		}
	})
}

func TestRemoveOwnPIDFile(t *testing.T) {
	t.Run("自分の pid が書いてあれば消す", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "daemon.pid")
		if err := WritePIDFile(path, 100); err != nil {
			t.Fatalf("セットアップに失敗: %v", err)
		}
		if err := RemoveOwnPIDFile(path, 100); err != nil {
			t.Fatalf("RemoveOwnPIDFile() error = %v", err)
		}
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("ファイルが削除されていない: err = %v", err)
		}
	})

	t.Run("別の pid が書いてあれば残す", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "daemon.pid")
		if err := WritePIDFile(path, 200); err != nil {
			t.Fatalf("セットアップに失敗: %v", err)
		}
		if err := RemoveOwnPIDFile(path, 100); err != nil {
			t.Fatalf("RemoveOwnPIDFile() error = %v", err)
		}
		got, err := ReadPIDFile(path)
		if err != nil {
			t.Fatalf("ReadPIDFile() error = %v", err)
		}
		if got != 200 {
			t.Errorf("ReadPIDFile() = %d, want 200 (他の daemon の pid file を消してはいけない)", got)
		}
	})

	t.Run("無ければエラーにしない", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "no-such-file")
		if err := RemoveOwnPIDFile(path, 100); err != nil {
			t.Errorf("RemoveOwnPIDFile() error = %v, want nil", err)
		}
	})
}
