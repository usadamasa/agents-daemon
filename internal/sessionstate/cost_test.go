package sessionstate

import (
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// costNow は月の途中の日付に固定する｡「前日」が同じ月に収まり､月初の分岐を避けるため｡
var costNow = time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)

const (
	costToday     = "2026-09-15"
	costYesterday = "2026-09-14"
	costLastMonth = "2026-08-20"
)

// seedCost は cost/<day>/<sid>.json に過去の記録を置く｡
func seedCost(t *testing.T, s Store, day, sessionID string, total, carried float64) {
	t.Helper()
	writeFile(t, filepath.Join(s.Cost, day, sessionID+".json"),
		`{"session_id":"`+sessionID+`","total_cost_usd":`+ftoa(total)+`,"carried_usd":`+ftoa(carried)+`}`+"\n")
}

func ftoa(v float64) string {
	return formatUSD(v)
}

func nearly(a, b float64) bool {
	return math.Abs(a-b) < 1e-9
}

func mustSumCost(t *testing.T, s Store) Spend {
	t.Helper()
	got, err := s.SumCost(costNow)
	if err != nil {
		t.Fatalf("SumCost() error = %v", err)
	}
	return got
}

func TestRecordCost(t *testing.T) {
	t.Run("その日の記録を作り､合算はその日と当月に同じ額が入る", func(t *testing.T) {
		s := New(t.TempDir())
		if err := s.RecordCost("sess-A", 3.79, costNow); err != nil {
			t.Fatalf("RecordCost() error = %v", err)
		}
		got := readFileString(t, filepath.Join(s.Cost, costToday, "sess-A.json"))
		want := `{"session_id":"sess-A","total_cost_usd":3.79,"carried_usd":0}` + "\n"
		if got != want {
			t.Errorf("cost file = %q, want %q", got, want)
		}
		spend := mustSumCost(t, s)
		if !nearly(spend.Day, 3.79) || !nearly(spend.Month, 3.79) {
			t.Errorf("SumCost() = %+v, want Day 3.79 Month 3.79", spend)
		}
	})

	t.Run("session_id が空なら何も書かない", func(t *testing.T) {
		s := New(t.TempDir())
		if err := s.RecordCost("", 3.79, costNow); err != nil {
			t.Fatalf("RecordCost() error = %v", err)
		}
		if _, err := os.Stat(s.Cost); !os.IsNotExist(err) {
			t.Errorf("cost/ が作られている: %v", err)
		}
	})

	t.Run("同じ日の別セッションのぶんを足す", func(t *testing.T) {
		s := New(t.TempDir())
		seedCost(t, s, costToday, "sess-B", 10.50, 0)
		if err := s.RecordCost("sess-A", 3.79, costNow); err != nil {
			t.Fatalf("RecordCost() error = %v", err)
		}
		spend := mustSumCost(t, s)
		if !nearly(spend.Day, 14.29) || !nearly(spend.Month, 14.29) {
			t.Errorf("SumCost() = %+v, want Day 14.29 Month 14.29", spend)
		}
	})

	t.Run("当月の別の日のぶんは Day に入らず Month に入る", func(t *testing.T) {
		s := New(t.TempDir())
		seedCost(t, s, costYesterday, "sess-B", 10.50, 0)
		if err := s.RecordCost("sess-A", 3.79, costNow); err != nil {
			t.Fatalf("RecordCost() error = %v", err)
		}
		spend := mustSumCost(t, s)
		if !nearly(spend.Day, 3.79) || !nearly(spend.Month, 14.29) {
			t.Errorf("SumCost() = %+v, want Day 3.79 Month 14.29", spend)
		}
	})

	t.Run("先月のぶんは当月の合計に入らない", func(t *testing.T) {
		s := New(t.TempDir())
		seedCost(t, s, costLastMonth, "sess-B", 500, 0)
		if err := s.RecordCost("sess-A", 3.79, costNow); err != nil {
			t.Fatalf("RecordCost() error = %v", err)
		}
		spend := mustSumCost(t, s)
		if !nearly(spend.Month, 3.79) {
			t.Errorf("SumCost().Month = %v, want 3.79", spend.Month)
		}
	})

	t.Run("日を跨いだセッションは前日までに計上した分を差し引く", func(t *testing.T) {
		s := New(t.TempDir())
		seedCost(t, s, costYesterday, "sess-A", 2.00, 0)
		if err := s.RecordCost("sess-A", 3.79, costNow); err != nil {
			t.Fatalf("RecordCost() error = %v", err)
		}
		got := readFileString(t, filepath.Join(s.Cost, costToday, "sess-A.json"))
		want := `{"session_id":"sess-A","total_cost_usd":3.79,"carried_usd":2}` + "\n"
		if got != want {
			t.Errorf("cost file = %q, want %q", got, want)
		}
		spend := mustSumCost(t, s)
		if !nearly(spend.Day, 1.79) || !nearly(spend.Month, 3.79) {
			t.Errorf("SumCost() = %+v, want Day 1.79 Month 3.79", spend)
		}
	})

	t.Run("前日までの記録が既に差し引き済みなら､その分だけを繰り越す", func(t *testing.T) {
		// 一昨日 2.00､昨日は累計 5.00 (繰り越し 2.00) → 計上済みは 5.00｡今日の累計 7.00 なら今日は 2.00｡
		s := New(t.TempDir())
		seedCost(t, s, "2026-09-13", "sess-A", 2.00, 0)
		seedCost(t, s, costYesterday, "sess-A", 5.00, 2.00)
		if err := s.RecordCost("sess-A", 7.00, costNow); err != nil {
			t.Fatalf("RecordCost() error = %v", err)
		}
		spend := mustSumCost(t, s)
		if !nearly(spend.Day, 2.00) || !nearly(spend.Month, 7.00) {
			t.Errorf("SumCost() = %+v, want Day 2 Month 7", spend)
		}
	})

	t.Run("保持期間より古い日は消え､差し引きにも混ざらない", func(t *testing.T) {
		s := New(t.TempDir())
		seedCost(t, s, "2020-01-01", "sess-A", 99.00, 0)
		seedCost(t, s, costYesterday, "sess-A", 2.00, 0)
		if err := s.RecordCost("sess-A", 3.79, costNow); err != nil {
			t.Fatalf("RecordCost() error = %v", err)
		}
		spend := mustSumCost(t, s)
		if !nearly(spend.Day, 1.79) {
			t.Errorf("SumCost().Day = %v, want 1.79", spend.Day)
		}
		if _, err := os.Stat(filepath.Join(s.Cost, "2020-01-01")); !os.IsNotExist(err) {
			t.Errorf("2020-01-01 が残っている: %v", err)
		}
		if _, err := os.Stat(filepath.Join(s.Cost, costYesterday)); err != nil {
			t.Errorf("前日のディレクトリが消えている: %v", err)
		}
	})

	t.Run("値が変わらない再描画では書き直さない", func(t *testing.T) {
		s := New(t.TempDir())
		if err := s.RecordCost("sess-A", 3.79, costNow); err != nil {
			t.Fatalf("RecordCost() error = %v", err)
		}
		path := filepath.Join(s.Cost, costToday, "sess-A.json")
		ageFile(t, path, time.Hour, costNow)
		before, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.RecordCost("sess-A", 3.79, costNow); err != nil {
			t.Fatalf("RecordCost() error = %v", err)
		}
		after, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if !after.ModTime().Equal(before.ModTime()) {
			t.Error("同じ値なのに書き直されている")
		}
	})

	t.Run("値が変わったら繰り越しを保ったまま書き直す", func(t *testing.T) {
		s := New(t.TempDir())
		seedCost(t, s, costYesterday, "sess-A", 2.00, 0)
		if err := s.RecordCost("sess-A", 3.79, costNow); err != nil {
			t.Fatalf("RecordCost() error = %v", err)
		}
		if err := s.RecordCost("sess-A", 4.10, costNow); err != nil {
			t.Fatalf("RecordCost() error = %v", err)
		}
		got := readFileString(t, filepath.Join(s.Cost, costToday, "sess-A.json"))
		want := `{"session_id":"sess-A","total_cost_usd":4.1,"carried_usd":2}` + "\n"
		if got != want {
			t.Errorf("cost file = %q, want %q", got, want)
		}
	})
}

func TestSumCost(t *testing.T) {
	t.Run("記録が無ければ 0", func(t *testing.T) {
		s := New(t.TempDir())
		spend := mustSumCost(t, s)
		if spend.Day != 0 || spend.Month != 0 {
			t.Errorf("SumCost() = %+v, want zero", spend)
		}
	})

	t.Run("壊れたファイルと日付でない名前は読み飛ばす", func(t *testing.T) {
		s := New(t.TempDir())
		seedCost(t, s, costToday, "sess-A", 3.79, 0)
		writeFile(t, filepath.Join(s.Cost, costToday, "broken.json"), "{")
		writeFile(t, filepath.Join(s.Cost, "notes.txt"), "x")
		spend := mustSumCost(t, s)
		if !nearly(spend.Day, 3.79) || !nearly(spend.Month, 3.79) {
			t.Errorf("SumCost() = %+v, want Day 3.79 Month 3.79", spend)
		}
	})

	t.Run("ゼロ値の Store は 0 を返す", func(t *testing.T) {
		var s Store
		spend := mustSumCost(t, s)
		if spend.Day != 0 || spend.Month != 0 {
			t.Errorf("SumCost() = %+v, want zero", spend)
		}
	})
}
