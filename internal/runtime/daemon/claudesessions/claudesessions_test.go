package claudesessions

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Names は <config>/sessions/<pid>.json から session ID → ListAgents の名前を引く。名前か session ID の無いもの、
// 壊れたもの、.json でないものは読み飛ばす。
func TestNames(t *testing.T) {
	config := t.TempDir()
	dir := filepath.Join(config, "sessions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, dir, "100.json", `{"pid":100,"sessionId":"S1","name":"agents-daemon-4b","nameSource":"derived"}`)
	write(t, dir, "200.json", `{"pid":200,"sessionId":"S2","name":"peer-b","nameSource":"user"}`)
	write(t, dir, "300.json", `{"pid":300,"sessionId":"S3"}`)
	write(t, dir, "400.json", `{"pid":400,"name":"no-id"}`)
	write(t, dir, "500.json", `{broken`)
	write(t, dir, "600.txt", `{"sessionId":"S6","name":"txt"}`)

	got, err := New(config).Names()
	if err != nil {
		t.Fatalf("Names() error = %v", err)
	}
	want := map[string]string{"S1": "agents-daemon-4b", "S2": "peer-b"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Names() = %v, want %v", got, want)
	}
}

// 登録簿が無い・ゼロ値の Store は空を返す。
func TestNames_missing(t *testing.T) {
	for _, s := range []Store{New(t.TempDir()), {}} {
		got, err := s.Names()
		if err != nil || len(got) != 0 {
			t.Errorf("Names() = %v, %v, want 空", got, err)
		}
	}
}
