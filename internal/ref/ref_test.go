package ref

import "testing"

func TestConstructors(t *testing.T) {
	for got, want := range map[string]string{
		Task("S1", "3"):   "task:S1/3",
		Todo("S1", 0):     "todo:S1/0",
		SessionID("S1"):   "sid:S1",
		Task("2e0b", "9"): "task:2e0b/9",
	} {
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
}

func TestParseTask(t *testing.T) {
	if sid, tid, ok := ParseTask("task:S1/3"); !ok || sid != "S1" || tid != "3" {
		t.Errorf("ParseTask(task:S1/3) = %q, %q, %v", sid, tid, ok)
	}
	for _, r := range []string{"", "task:S1", "task:/3", "task:S1/", "task:S1/3/4", "todo:S1/0", "sid:S1", "S1/3", "task:S 1/3"} {
		if _, _, ok := ParseTask(r); ok {
			t.Errorf("ParseTask(%q) は task の参照ではない", r)
		}
	}
}

func TestParseItem(t *testing.T) {
	for r, want := range map[string]Item{
		"pr:usadamasa/agents-daemon#389": {Kind: "pr", Repo: "usadamasa/agents-daemon", Number: 389},
		"issue:o/r.x#1":                  {Kind: "issue", Repo: "o/r.x", Number: 1},
	} {
		if got, ok := ParseItem(r); !ok || got != want {
			t.Errorf("ParseItem(%q) = %+v, %v, want %+v", r, got, ok, want)
		}
	}
	for _, r := range []string{"", "pr:#12", "issue:o/r", "pr:o/r#", "task:S1/3", "sid:S1", "pull:o/r#1"} {
		if _, ok := ParseItem(r); ok {
			t.Errorf("ParseItem(%q) は issue / PR の参照ではない", r)
		}
	}
}

// Title は「PR #389 · owner/repo」の形。
func TestItemTitle(t *testing.T) {
	if got := (Item{Kind: "pr", Repo: "o/r", Number: 389}).Title(); got != "PR #389 · o/r" {
		t.Errorf("Title = %q", got)
	}
	if got := (Item{Kind: "issue", Repo: "o/r", Number: 1}).Title(); got != "issue #1 · o/r" {
		t.Errorf("Title = %q", got)
	}
}

// 辺の端点にできるのは todo 以外の形。todo は並びが変わると別の項目を指す。
func TestEndpoint(t *testing.T) {
	for _, r := range []string{"session:ad-134", "sid:2e0b-11", "issue:usadamasa/agents-daemon#134", "pr:usadamasa/agents-config.x#7", "task:2e0b-11/3"} {
		if !Endpoint(r) {
			t.Errorf("Endpoint(%q) = false, want true", r)
		}
	}
	for _, r := range []string{"", "a", "branch:main", "session:", "session:a b", "sid:", "sid:a/b", "issue:usadamasa/agents-daemon", "pr:#12", "task:s1", "todo:s1/0"} {
		if Endpoint(r) {
			t.Errorf("Endpoint(%q) = true, want false", r)
		}
	}
}
