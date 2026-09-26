package applog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// newTestLogger は一時ディレクトリ配下の daemon.log へ書く Logger と､そのパスを返す｡
func newTestLogger(t *testing.T, now func() time.Time) (*Logger, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "daemon.log")
	log := NewLogger(path, now)
	t.Cleanup(func() { _ = log.Close() })
	return log, path
}

func readLog(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path) // #nosec G304 -- テストの一時ディレクトリ配下
	if err != nil {
		t.Fatalf("%s が読めない: %v", filepath.Base(path), err)
	}
	return string(data)
}

func touch(t *testing.T, path, content string, mtime time.Time) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("セットアップに失敗: %v", err)
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatalf("mtime の設定に失敗: %v", err)
	}
}

func TestLogger_AppendsToFixedFile(t *testing.T) {
	cur := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	log, path := newTestLogger(t, func() time.Time { return cur })

	log.Logf("1行目")
	log.Logf("2行目")

	data := readLog(t, path)
	lines := strings.Split(strings.TrimRight(data, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("行数 = %d, want 2 (truncate されず append されているはず): %q", len(lines), data)
	}
	if !strings.HasPrefix(lines[0], "2026-01-01T10:00:00Z 1行目") {
		t.Errorf("1 行目の形式が想定外: %q", lines[0])
	}
}

func TestLogger_RotatesAtMidnight(t *testing.T) {
	cur := time.Date(2026, 1, 1, 23, 59, 0, 0, time.UTC)
	log, path := newTestLogger(t, func() time.Time { return cur })

	log.Logf("最初の日のメッセージ")

	cur = time.Date(2026, 1, 2, 0, 0, 1, 0, time.UTC)
	log.Logf("次の日のメッセージ")

	rotated := readLog(t, RotatedPath(path, "2026-01-01"))
	if !strings.Contains(rotated, "最初の日のメッセージ") {
		t.Errorf("rotated の中身が想定外: %q", rotated)
	}
	if strings.Contains(rotated, "次の日のメッセージ") {
		t.Errorf("rotated に次の日のメッセージが混入している: %q", rotated)
	}

	current := readLog(t, path)
	if !strings.Contains(current, "次の日のメッセージ") {
		t.Errorf("daemon.log の中身が想定外: %q", current)
	}
	if strings.Contains(current, "最初の日のメッセージ") {
		t.Errorf("daemon.log に前の日のメッセージが残っている: %q", current)
	}
}

func TestLogger_RotatesStaleFileLeftByPreviousProcess(t *testing.T) {
	// daemon の再起動を跨いで日付が変わっていた場合､前のプロセスが残した
	// daemon.log は最終更新日 (mtime) の名前へ退避してから書き始める｡
	cur := time.Date(2026, 1, 3, 9, 0, 0, 0, time.UTC)
	log, path := newTestLogger(t, func() time.Time { return cur })
	touch(t, path, "前のプロセスの行\n", time.Date(2026, 1, 1, 20, 0, 0, 0, time.UTC))

	log.Logf("新しいプロセスの行")

	rotated := readLog(t, RotatedPath(path, "2026-01-01"))
	if !strings.Contains(rotated, "前のプロセスの行") {
		t.Errorf("rotated の中身が想定外: %q", rotated)
	}
	now := readLog(t, path)
	if strings.Contains(now, "前のプロセスの行") || !strings.Contains(now, "新しいプロセスの行") {
		t.Errorf("daemon.log の中身が想定外: %q", now)
	}
}

func TestLogger_AppendsAcrossInstancesWithinSameDay(t *testing.T) {
	// 同じ日のうちの再起動では rotate せず追記する｡
	day := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	now := func() time.Time { return day }
	log1, path := newTestLogger(t, now)
	log1.Logf("1個目のロガーからの行")
	if err := log1.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	// mtime を「当日」に固定する (テストの実時刻とロガーの now がずれるため)｡
	if err := os.Chtimes(path, day, day); err != nil {
		t.Fatalf("mtime の設定に失敗: %v", err)
	}

	log2 := NewLogger(path, now)
	log2.Logf("2個目のロガーからの行")
	if err := log2.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	data := readLog(t, path)
	if !strings.Contains(data, "1個目のロガーからの行") || !strings.Contains(data, "2個目のロガーからの行") {
		t.Errorf("両方の行が残っているべき: %q", data)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("同じ日のうちは rotate しないはず: %d ファイル", len(entries))
	}
}

func TestLogger_KeepsAppendingWhenRotatedNameIsTaken(t *testing.T) {
	// 退避先が既にあるときは上書きせず､次の日付境界まで daemon.log へ追記し続ける｡
	cur := time.Date(2026, 1, 1, 23, 0, 0, 0, time.UTC)
	log, path := newTestLogger(t, func() time.Time { return cur })
	taken := RotatedPath(path, "2026-01-01")
	touch(t, taken, "既存の退避ファイル\n", cur)

	log.Logf("1日目")
	cur = time.Date(2026, 1, 2, 1, 0, 0, 0, time.UTC)
	log.Logf("2日目")

	current := readLog(t, path)
	if !strings.Contains(current, "1日目") || !strings.Contains(current, "2日目") {
		t.Errorf("daemon.log に両日の行が残るべき: %q", current)
	}
	if got := readLog(t, taken); got != "既存の退避ファイル\n" {
		t.Errorf("既存の退避ファイルが書き換わっている: %q", got)
	}
}

func TestLogger_CloseWithoutWrite(t *testing.T) {
	log, _ := newTestLogger(t, time.Now)
	if err := log.Close(); err != nil {
		t.Errorf("Close() error = %v, want nil", err)
	}
}

func TestPruneRotated(t *testing.T) {
	now := time.Date(2026, 1, 10, 12, 0, 0, 0, time.UTC)
	const keep = 7 * 24 * time.Hour

	t.Run("保持期間を過ぎた rotated だけを消す", func(t *testing.T) {
		dir := t.TempDir()
		current := filepath.Join(dir, "daemon.log")
		files := map[string]time.Time{
			RotatedPath(current, "2025-12-20"):   now.Add(-20 * 24 * time.Hour), // 消える
			RotatedPath(current, "2026-01-02"):   now.Add(-8 * 24 * time.Hour),  // 消える
			RotatedPath(current, "2026-01-08"):   now.Add(-2 * 24 * time.Hour),  // 残る
			current:                              now.Add(-30 * 24 * time.Hour), // 現行ファイルは対象外
			filepath.Join(dir, "daemon.log.bak"): now.Add(-30 * 24 * time.Hour), // 日付でない後置は触らない
			filepath.Join(dir, "unrelated.txt"):  now.Add(-30 * 24 * time.Hour), // 命名規則外は触らない
		}
		for path, mtime := range files {
			touch(t, path, "x\n", mtime)
		}

		removed, err := PruneRotated(current, now, keep)
		if err != nil {
			t.Fatalf("PruneRotated() error = %v", err)
		}
		if removed != 2 {
			t.Errorf("removed = %d, want 2", removed)
		}
		for _, path := range []string{RotatedPath(current, "2026-01-08"), current, filepath.Join(dir, "daemon.log.bak"), filepath.Join(dir, "unrelated.txt")} {
			if _, err := os.Stat(path); err != nil {
				t.Errorf("%s は残るべき: %v", filepath.Base(path), err)
			}
		}
		for _, path := range []string{RotatedPath(current, "2025-12-20"), RotatedPath(current, "2026-01-02")} {
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Errorf("%s は消えるべき (err=%v)", filepath.Base(path), err)
			}
		}
	})

	t.Run("ディレクトリが無ければ何もしない", func(t *testing.T) {
		removed, err := PruneRotated(filepath.Join(t.TempDir(), "no-such", "daemon.log"), now, keep)
		if err != nil {
			t.Fatalf("PruneRotated() error = %v, want nil", err)
		}
		if removed != 0 {
			t.Errorf("removed = %d, want 0", removed)
		}
	})
}
