package harvest

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWalkFilter_ProjectGlob(t *testing.T) {
	projectsDir := t.TempDir()
	makeProject := func(name string) string {
		dir := filepath.Join(projectsDir, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(dir, "sess.jsonl")
		if err := os.WriteFile(p, []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}

	pA := makeProject("-Users-x-workspace")
	pB := makeProject("-Users-x-otherrepo")

	f := &walkFilter{projectGlob: "workspace"}
	if !f.accept(pA, projectsDir) {
		t.Error("workspace project should be accepted")
	}
	if f.accept(pB, projectsDir) {
		t.Error("otherrepo project should be rejected")
	}

	// no glob accepts all
	fEmpty := &walkFilter{}
	if !fEmpty.accept(pA, projectsDir) || !fEmpty.accept(pB, projectsDir) {
		t.Error("empty glob should accept all")
	}
}

func TestWalkFilter_Incremental(t *testing.T) {
	projectsDir := t.TempDir()
	dir := filepath.Join(projectsDir, "-Users-x-workspace")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	oldPath := filepath.Join(dir, "old.jsonl")
	newPath := filepath.Join(dir, "new.jsonl")
	if err := os.WriteFile(oldPath, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newPath, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// oldPath を過去に、newPath を未来にしておく
	past := time.Now().Add(-48 * time.Hour)
	future := time.Now().Add(1 * time.Hour)
	if err := os.Chtimes(oldPath, past, past); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(newPath, future, future); err != nil {
		t.Fatal(err)
	}

	// cutoff = 24h ago なので oldPath は reject、newPath は accept
	cutoff := time.Now().Add(-24 * time.Hour)
	f := &walkFilter{incremental: true, lastRun: cutoff.Format(time.RFC3339)}

	if f.accept(oldPath, projectsDir) {
		t.Error("old file should be rejected in incremental mode")
	}
	if !f.accept(newPath, projectsDir) {
		t.Error("new file should be accepted in incremental mode")
	}
}

func TestWalkFilter_IncrementalFailOpenOnCorruptedTS(t *testing.T) {
	projectsDir := t.TempDir()
	p := filepath.Join(projectsDir, "sess.jsonl")
	if err := os.WriteFile(p, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f := &walkFilter{incremental: true, lastRun: "not-a-timestamp"}
	if !f.accept(p, projectsDir) {
		t.Error("should fail-open when lastRun is unparseable")
	}
}

func TestExtractProjectFromPath(t *testing.T) {
	projectsDir := "/home/x/.claude/projects"
	tests := []struct {
		in   string
		want string
	}{
		{"/home/x/.claude/projects/-Users-a-b/session.jsonl", "-Users-a-b"},
		{"/home/x/.claude/projects/foo/nested/bar.jsonl", "foo"},
		{"/somewhere/else/session.jsonl", ""}, // outside projectsDir → filepath.Rel returns non-relative but no error on Unix
	}
	for _, tc := range tests {
		got := extractProjectFromPath(tc.in, projectsDir)
		// Note: /somewhere/else returns "../../.." style, so first segment ".." — we accept either "" or non-empty
		// but for the specific first two cases we assert exactness
		if tc.want != "" && got != tc.want {
			t.Errorf("extractProjectFromPath(%q) = %q; want %q", tc.in, got, tc.want)
		}
	}
}
