package parking

import "testing"

// 取る (handed にする) ときは宛先を同じ Set で書き、戻す・捨てるときは空にする。handed には宛先が要り、
// handed 以外には宛先を書かない (models/handoff の HandedHasDestination / OnlyHandedHasDestination)。
func TestSet_handedTo(t *testing.T) {
	s := New(t.TempDir())
	item := mustAdd(t, s, testRepo, aside("t"), source("s1", ""))

	got, _, err := s.Set(item.ID, StatusHanded, StatusOpen, ToSession("S2"))
	if err != nil || got.HandedTo != "sid:S2" {
		t.Fatalf("Set(handed) = %+v, %v", got, err)
	}
	if list, _ := s.list(); list[0].HandedTo != "sid:S2" {
		t.Errorf("宛先が書き戻されていない: %+v", list[0])
	}
	back, _, err := s.Set(item.ID, StatusOpen, StatusHanded, "")
	if err != nil || back.HandedTo != "" {
		t.Fatalf("戻した Set() = %+v, %v", back, err)
	}
	spawned, _, err := s.Set(item.ID, StatusHanded, StatusOpen, ToRepo("/w/dotfile"))
	if err != nil || spawned.HandedTo != "repo:/w/dotfile" {
		t.Fatalf("起こす先の repo へ取った Set() = %+v, %v", spawned, err)
	}
	discarded, _, err := s.Set(item.ID, StatusDiscarded, "", "")
	if err != nil || discarded.HandedTo != "" {
		t.Errorf("捨てた Set() = %+v, %v", discarded, err)
	}
}

func TestSet_rejectsDestination(t *testing.T) {
	s := New(t.TempDir())
	item := mustAdd(t, s, testRepo, aside("t"), source("s1", ""))
	cases := []struct {
		name, status, to string
	}{
		{"handed に宛先が無い", StatusHanded, ""},
		{"open に宛先がある", StatusOpen, ToSession("S2")},
		{"discarded に宛先がある", StatusDiscarded, ToSession("S2")},
		{"宛先の形が違う", StatusHanded, "S2"},
		{"session ID がファイル名に使えない", StatusHanded, "sid:../x"},
		{"repo が相対パス", StatusHanded, "repo:w/dotfile"},
		{"repo が空", StatusHanded, "repo:"},
	}
	for _, c := range cases {
		if _, _, err := s.Set(item.ID, c.status, "", c.to); err == nil {
			t.Errorf("%s: Set() が通った", c.name)
		}
	}
	if list, _ := s.list(); list[0].Status != StatusOpen || list[0].HandedTo != "" {
		t.Errorf("失敗した Set が書き換えた: %+v", list[0])
	}
}

// model が置いたメモの出自は placed。同じメモにまとめても出自は最初のメモのまま。
func TestAdd_origin(t *testing.T) {
	s := New(t.TempDir())
	first := mustAdd(t, s, testRepo, aside("t"), source("s1", ""))
	if first.Origin != OriginPlaced {
		t.Errorf("origin = %q, want placed", first.Origin)
	}
	merged, err := s.add(testRepo, aside("t"), OriginAdopted, source("s2", "4"))
	if err != nil || merged.Created || merged.Item.Origin != OriginPlaced {
		t.Errorf("まとめた add() = %+v, %v", merged, err)
	}
}
