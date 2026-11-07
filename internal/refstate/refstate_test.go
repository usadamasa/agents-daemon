package refstate

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

var t0 = time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)

func TestStore_PutとStates(t *testing.T) {
	s := New(t.TempDir())
	for _, rec := range []Record{
		{Ref: "pr:o/r#1", State: StateMerged, CheckedAt: t0},
		{Ref: "issue:o/r#2", State: StateOpen, CheckedAt: t0},
		{Ref: "pr:o/r#1", State: StateMerged, CheckedAt: t0.Add(time.Minute)},
	} {
		if err := s.Put(rec); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]Record{
		"pr:o/r#1":    {Ref: "pr:o/r#1", State: StateMerged, CheckedAt: t0.Add(time.Minute)},
		"issue:o/r#2": {Ref: "issue:o/r#2", State: StateOpen, CheckedAt: t0},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("List = %v, want %v", got, want)
	}
	states, err := s.States()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(states, map[string]string{"pr:o/r#1": StateMerged, "issue:o/r#2": StateOpen}) {
		t.Errorf("States = %v", states)
	}
}

func TestStore_Put_知らない状態は書かない(t *testing.T) {
	s := New(t.TempDir())
	for _, rec := range []Record{
		{Ref: "pr:o/r#1", State: "draft", CheckedAt: t0},
		{Ref: "sid:abc", State: StateOpen, CheckedAt: t0},
	} {
		if err := s.Put(rec); err == nil {
			t.Errorf("Put(%+v) が通った", rec)
		}
	}
}

func TestStore_ゼロ値と無い置き場(t *testing.T) {
	if got, err := (Store{}).List(); err != nil || len(got) != 0 {
		t.Errorf("ゼロ値の List = %v, %v", got, err)
	}
	if got, err := New(filepath.Join(t.TempDir(), "none")).List(); err != nil || len(got) != 0 {
		t.Errorf("無い置き場の List = %v, %v", got, err)
	}
	if err := (Store{}).Put(Record{Ref: "pr:o/r#1", State: StateOpen}); err == nil {
		t.Error("ゼロ値の Put が通った")
	}
}

func TestStore_壊れたファイルと一時ファイルは読み飛ばす(t *testing.T) {
	s := New(t.TempDir())
	if err := s.Put(Record{Ref: "pr:o/r#1", State: StateClosed, CheckedAt: t0}); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"broken.json": "{", ".ref.123": "{}", "empty.json": `{"state":"open"}`} {
		if err := os.WriteFile(filepath.Join(s.Dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got["pr:o/r#1"].State != StateClosed {
		t.Errorf("List = %v", got)
	}
}

func TestStore_Prune(t *testing.T) {
	s := New(t.TempDir())
	if err := s.Put(Record{Ref: "pr:o/r#1", State: StateMerged, CheckedAt: t0}); err != nil {
		t.Fatal(err)
	}
	if n, err := s.Prune(time.Now().Add(Retention - time.Hour)); err != nil || n != 0 {
		t.Errorf("保持期間の中の Prune = %d, %v", n, err)
	}
	if n, err := s.Prune(time.Now().Add(Retention + time.Hour)); err != nil || n != 1 {
		t.Errorf("保持期間を過ぎた Prune = %d, %v", n, err)
	}
}

// Due はモデル (models/refstate) の Due: 写しが無いか open の参照だけを引き直し、merged / closed は引かない。
// open は前回から RefreshInterval 経つまで間引く。
func TestDue(t *testing.T) {
	cases := []struct {
		name  string
		rec   Record
		found bool
		now   time.Time
		want  bool
	}{
		{"写しが無い", Record{}, false, t0, true},
		{"open で間隔の中", Record{State: StateOpen, CheckedAt: t0}, true, t0.Add(RefreshInterval - time.Second), false},
		{"open で間隔を過ぎた", Record{State: StateOpen, CheckedAt: t0}, true, t0.Add(RefreshInterval), true},
		{"merged は引かない", Record{State: StateMerged, CheckedAt: t0}, true, t0.Add(100 * RefreshInterval), false},
		{"closed は引かない", Record{State: StateClosed, CheckedAt: t0}, true, t0.Add(100 * RefreshInterval), false},
		{"取りやめは引かない", Record{State: StateDropped, CheckedAt: t0}, true, t0.Add(100 * RefreshInterval), false},
	}
	for _, c := range cases {
		if got := Due(c.rec, c.found, c.now); got != c.want {
			t.Errorf("%s: Due = %v, want %v", c.name, got, c.want)
		}
	}
}

// 済んだ (Done) のは merged と closed (completed で閉じた issue) だけ。取りやめは閉じているが済んでいない。
func TestDoneとSettled(t *testing.T) {
	cases := map[string][2]bool{
		StateMerged:  {true, true},
		StateClosed:  {true, true},
		StateDropped: {false, true},
		StateOpen:    {false, false},
		"":           {false, false},
	}
	for state, want := range cases {
		if Done(state) != want[0] || Settled(state) != want[1] {
			t.Errorf("Done(%q), Settled(%q) = %v, %v, want %v", state, state, Done(state), Settled(state), want)
		}
	}
}

func TestStore_Put_取りやめを書く(t *testing.T) {
	s := New(t.TempDir())
	if err := s.Put(Record{Ref: "pr:o/r#1", State: StateDropped, CheckedAt: t0}); err != nil {
		t.Fatal(err)
	}
	if got, err := s.States(); err != nil || got["pr:o/r#1"] != StateDropped {
		t.Errorf("States = %v, %v", got, err)
	}
}
