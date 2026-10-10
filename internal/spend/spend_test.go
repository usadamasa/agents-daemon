package spend

import (
	"math"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// testNow は月の途中の日付に固定する｡「前日」が同じ月に収まり､月初の分岐を避けるため｡
var testNow = time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)

const (
	today     = "2026-09-15"
	yesterday = "2026-09-14"
	lastMonth = "2026-08-20"
)

func newLedger(t *testing.T) Ledger {
	t.Helper()
	return New(t.TempDir())
}

// seed は <day>/<sid>.json に過去の記録を置く｡
func seed(t *testing.T, l Ledger, day, sessionID string, total, carried float64) {
	t.Helper()
	writeFile(t, filepath.Join(l.Dir, day, sessionID+".json"),
		`{"session_id":"`+sessionID+`","total_cost_usd":`+ftoa(total)+`,"carried_usd":`+ftoa(carried)+`}`+"\n")
}

// ftoa は JSON と同じ最短表記 (3.79､2､4.1) にする｡
func ftoa(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}

func readFileString(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", path, err)
	}
	return string(data)
}

func nearly(a, b float64) bool {
	return math.Abs(a-b) < 1e-9
}

func mustSum(t *testing.T, l Ledger) Spend {
	t.Helper()
	got, err := l.Sum(testNow)
	if err != nil {
		t.Fatalf("Sum() error = %v", err)
	}
	return got
}

func mustRecord(t *testing.T, l Ledger, sessionID string, total float64) {
	t.Helper()
	if err := l.Record(sessionID, total, testNow); err != nil {
		t.Fatalf("Record() error = %v", err)
	}
}

func TestRecord(t *testing.T) {
	t.Run("その日の記録を作り､合算はその日と当月に同じ額が入る", func(t *testing.T) {
		l := newLedger(t)
		mustRecord(t, l, "sess-A", 3.79)
		got := readFileString(t, filepath.Join(l.Dir, today, "sess-A.json"))
		want := `{"session_id":"sess-A","total_cost_usd":3.79,"carried_usd":0}` + "\n"
		if got != want {
			t.Errorf("record = %q, want %q", got, want)
		}
		spend := mustSum(t, l)
		if !nearly(spend.Day, 3.79) || !nearly(spend.Month, 3.79) {
			t.Errorf("Sum() = %+v, want Day 3.79 Month 3.79", spend)
		}
	})

	t.Run("session_id が空なら何も書かない", func(t *testing.T) {
		l := newLedger(t)
		mustRecord(t, l, "", 3.79)
		if _, err := os.Stat(l.Dir); !os.IsNotExist(err) {
			t.Errorf("cost/ が作られている: %v", err)
		}
	})

	t.Run("ファイル名に使えない session_id はエラー", func(t *testing.T) {
		l := newLedger(t)
		if err := l.Record("../x", 3.79, testNow); err == nil {
			t.Error("Record() error = nil, want error")
		}
	})

	t.Run("同じ日の別セッションのぶんを足す", func(t *testing.T) {
		l := newLedger(t)
		seed(t, l, today, "sess-B", 10.50, 0)
		mustRecord(t, l, "sess-A", 3.79)
		spend := mustSum(t, l)
		if !nearly(spend.Day, 14.29) || !nearly(spend.Month, 14.29) {
			t.Errorf("Sum() = %+v, want Day 14.29 Month 14.29", spend)
		}
	})

	t.Run("当月の別の日のぶんは Day に入らず Month に入る", func(t *testing.T) {
		l := newLedger(t)
		seed(t, l, yesterday, "sess-B", 10.50, 0)
		mustRecord(t, l, "sess-A", 3.79)
		spend := mustSum(t, l)
		if !nearly(spend.Day, 3.79) || !nearly(spend.Month, 14.29) {
			t.Errorf("Sum() = %+v, want Day 3.79 Month 14.29", spend)
		}
	})

	t.Run("先月のぶんは当月の合計に入らない", func(t *testing.T) {
		l := newLedger(t)
		seed(t, l, lastMonth, "sess-B", 500, 0)
		mustRecord(t, l, "sess-A", 3.79)
		if spend := mustSum(t, l); !nearly(spend.Month, 3.79) {
			t.Errorf("Sum().Month = %v, want 3.79", spend.Month)
		}
	})

	t.Run("日を跨いだセッションは前日までに計上した分を差し引く", func(t *testing.T) {
		l := newLedger(t)
		seed(t, l, yesterday, "sess-A", 2.00, 0)
		mustRecord(t, l, "sess-A", 3.79)
		got := readFileString(t, filepath.Join(l.Dir, today, "sess-A.json"))
		want := `{"session_id":"sess-A","total_cost_usd":3.79,"carried_usd":2}` + "\n"
		if got != want {
			t.Errorf("record = %q, want %q", got, want)
		}
		spend := mustSum(t, l)
		if !nearly(spend.Day, 1.79) || !nearly(spend.Month, 3.79) {
			t.Errorf("Sum() = %+v, want Day 1.79 Month 3.79", spend)
		}
	})

	t.Run("前日までの記録が既に差し引き済みなら､その分だけを繰り越す", func(t *testing.T) {
		// 一昨日 2.00､昨日は累計 5.00 (繰り越し 2.00) → 計上済みは 5.00｡今日の累計 7.00 なら今日は 2.00｡
		l := newLedger(t)
		seed(t, l, "2026-09-13", "sess-A", 2.00, 0)
		seed(t, l, yesterday, "sess-A", 5.00, 2.00)
		mustRecord(t, l, "sess-A", 7.00)
		spend := mustSum(t, l)
		if !nearly(spend.Day, 2.00) || !nearly(spend.Month, 7.00) {
			t.Errorf("Sum() = %+v, want Day 2 Month 7", spend)
		}
	})

	t.Run("保持期間より古い日は消え､差し引きにも混ざらない", func(t *testing.T) {
		l := newLedger(t)
		seed(t, l, "2020-01-01", "sess-A", 99.00, 0)
		seed(t, l, yesterday, "sess-A", 2.00, 0)
		mustRecord(t, l, "sess-A", 3.79)
		if spend := mustSum(t, l); !nearly(spend.Day, 1.79) {
			t.Errorf("Sum().Day = %v, want 1.79", spend.Day)
		}
		if _, err := os.Stat(filepath.Join(l.Dir, "2020-01-01")); !os.IsNotExist(err) {
			t.Errorf("2020-01-01 が残っている: %v", err)
		}
		if _, err := os.Stat(filepath.Join(l.Dir, yesterday)); err != nil {
			t.Errorf("前日のディレクトリが消えている: %v", err)
		}
	})

	t.Run("値が変わらない再描画では書き直さない", func(t *testing.T) {
		l := newLedger(t)
		mustRecord(t, l, "sess-A", 3.79)
		path := filepath.Join(l.Dir, today, "sess-A.json")
		at := testNow.Add(-time.Hour)
		if err := os.Chtimes(path, at, at); err != nil {
			t.Fatal(err)
		}
		mustRecord(t, l, "sess-A", 3.79)
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if !info.ModTime().Equal(at) {
			t.Error("同じ値なのに書き直されている")
		}
	})

	t.Run("値が変わったら繰り越しを保ったまま書き直す", func(t *testing.T) {
		l := newLedger(t)
		seed(t, l, yesterday, "sess-A", 2.00, 0)
		mustRecord(t, l, "sess-A", 3.79)
		mustRecord(t, l, "sess-A", 4.10)
		got := readFileString(t, filepath.Join(l.Dir, today, "sess-A.json"))
		want := `{"session_id":"sess-A","total_cost_usd":4.1,"carried_usd":2}` + "\n"
		if got != want {
			t.Errorf("record = %q, want %q", got, want)
		}
	})
}

func TestSum(t *testing.T) {
	t.Run("記録が無ければ 0", func(t *testing.T) {
		if spend := mustSum(t, newLedger(t)); spend.Day != 0 || spend.Month != 0 {
			t.Errorf("Sum() = %+v, want zero", spend)
		}
	})

	t.Run("壊れたファイルと日付でない名前は読み飛ばす", func(t *testing.T) {
		l := newLedger(t)
		seed(t, l, today, "sess-A", 3.79, 0)
		writeFile(t, filepath.Join(l.Dir, today, "broken.json"), "{")
		writeFile(t, filepath.Join(l.Dir, "notes.txt"), "x")
		spend := mustSum(t, l)
		if !nearly(spend.Day, 3.79) || !nearly(spend.Month, 3.79) {
			t.Errorf("Sum() = %+v, want Day 3.79 Month 3.79", spend)
		}
	})

	t.Run("ゼロ値の Ledger は 0 を返す", func(t *testing.T) {
		if spend := mustSum(t, Ledger{}); spend.Day != 0 || spend.Month != 0 {
			t.Errorf("Sum() = %+v, want zero", spend)
		}
	})
}
