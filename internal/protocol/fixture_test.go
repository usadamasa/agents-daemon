package protocol

import (
	"os"
	"path/filepath"
	"testing"
)

// 依存図のアプリの UI テスト (gui/frontend/test/ui.test.js) が流す deps の fixture は手書きなので、schema を
// 変えたときに古い形のまま描画を確かめ続けないよう、DepsData として読めるかをここで確かめる。
func TestGUIFixtureIsDepsData(t *testing.T) {
	f, err := os.Open(filepath.Join("..", "..", "gui", "frontend", "test", "fixtures", "deps.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var got DepsData
	if err := Decode(f, "DepsData", &got); err != nil {
		t.Fatalf("fixture が DepsData の形でない: %v", err)
	}
}
