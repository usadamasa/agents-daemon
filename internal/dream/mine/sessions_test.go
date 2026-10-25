package mine

import (
	"testing"

	"github.com/usadamasa/agents-daemon/internal/dream/harvest/record"
)

func TestExtractInvokedSkills(t *testing.T) {
	recs := []record.Record{
		{SessionID: "s1", CWDProject: "-Users-x-workspace", SkillsInvoked: []string{"daily-standup", "caddi-obsidian"}},
		{SessionID: "s2", CWDProject: "-Users-x-workspace", SkillsInvoked: []string{"daily-standup", "weekly-report"}},
	}
	skills := extractInvokedSkills(recs)
	// deduped + sorted
	want := []string{"caddi-obsidian", "daily-standup", "weekly-report"}
	if len(skills) != len(want) {
		t.Fatalf("skills = %v; want %v", skills, want)
	}
	for i, s := range skills {
		if s != want[i] {
			t.Errorf("skills[%d] = %q; want %q", i, s, want[i])
		}
	}
}

func TestProjectRootFromSessions_PicksFirstCWD(t *testing.T) {
	sessions := []record.Record{
		{SessionID: "s1", CWD: "/Users/x/workspace"},
		{SessionID: "s2", CWD: "/Users/x/other"},
	}
	got := projectRootFromSessions(sessions)
	if got != "/Users/x/workspace" {
		t.Errorf("got %q; want /Users/x/workspace", got)
	}
}

func TestProjectRootFromSessions_EmptyReturnsEmpty(t *testing.T) {
	if got := projectRootFromSessions(nil); got != "" {
		t.Errorf("got %q; want ''", got)
	}
}
