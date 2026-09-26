package daemon

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// 実行ファイルの差し替えを daemon 自身が検知できることを固定する｡
//
// Go のバイナリは起動時にメモリへ読み込まれるため､hook がキャッシュ上の
// バイナリを差し替えても (または `task install` で入れ直しても)
// 動作中のプロセスには反映されない｡--ensure は「生きている daemon があれば
// 何もしない」ので､この検知が無いと直したコードが何日も動かないままになる
// (送信バグを直した後も古い daemon が動き続け､その晩の上限到達で自動再開に
// 失敗した｡この回帰テストはその再発を止めるためにある)｡
func TestBinaryStamp_changed(t *testing.T) {
	newStampedFile := func(t *testing.T, body string) (string, binaryStamp) {
		t.Helper()
		path := filepath.Join(t.TempDir(), "agents-daemon")
		if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
			t.Fatalf("実行ファイルの作成に失敗: %v", err)
		}
		s, err := stampFor(path)
		if err != nil {
			t.Fatalf("stampFor() error = %v", err)
		}
		return path, s
	}

	t.Run("差し替えが無ければ false", func(t *testing.T) {
		_, s := newStampedFile(t, "v1")
		if s.changed() {
			t.Error("changed() = true, want false (ファイルは変わっていない)")
		}
	})

	t.Run("サイズが変われば true", func(t *testing.T) {
		path, s := newStampedFile(t, "v1")
		if err := os.WriteFile(path, []byte("v2-longer"), 0o700); err != nil {
			t.Fatalf("上書きに失敗: %v", err)
		}
		if !s.changed() {
			t.Error("changed() = false, want true (サイズが変わった)")
		}
	})

	t.Run("サイズが同じでも mtime が変われば true", func(t *testing.T) {
		// ビルドが同一秒内に終わってもサイズが同じとは限らないが､両方見ることで
		// どちらか一方しか動かない環境でも取りこぼさない｡
		path, s := newStampedFile(t, "v1")
		if err := os.WriteFile(path, []byte("v2"), 0o700); err != nil {
			t.Fatalf("上書きに失敗: %v", err)
		}
		future := time.Now().Add(time.Minute)
		if err := os.Chtimes(path, future, future); err != nil {
			t.Fatalf("Chtimes() error = %v", err)
		}
		if !s.changed() {
			t.Error("changed() = false, want true (mtime が変わった)")
		}
	})

	t.Run("ファイルが消えていても false (動いている daemon を落とさない)", func(t *testing.T) {
		path, s := newStampedFile(t, "v1")
		if err := os.Remove(path); err != nil {
			t.Fatalf("Remove() error = %v", err)
		}
		if s.changed() {
			t.Error("changed() = true, want false (判定できないときは変更なしに倒す)")
		}
	})

	t.Run("ゼロ値の stamp は常に false (更新検知が無効な状態)", func(t *testing.T) {
		var s binaryStamp
		if s.changed() {
			t.Error("changed() = true, want false (path が空なら検知しない)")
		}
	})
}
