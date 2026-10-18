package adopt

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/usadamasa/agents-daemon/internal/dream/dreamio"
)

func TestRun_RejectsUnhomedCandidate(t *testing.T) {
	// unhomed は行き先が決まっていない候補｡branch を切る前に拒否する｡
	dir := t.TempDir()
	candFile := filepath.Join(dir, "candidates.jsonl")
	cands := []dreamio.Candidate{
		{
			ID:         "dream-candidate-unhomed-feedback-random-001",
			Kind:       "unhomed",
			MemoryPath: "/mem/x.md",
			Confidence: "unknown",
		},
	}
	if err := dreamio.WriteCandidates(candFile, cands); err != nil {
		t.Fatalf("WriteCandidates: %v", err)
	}

	err := Run(Opts{
		CandidateID:    cands[0].ID,
		CandidatesFile: candFile,
		DryRun:         true,
		Home:           dir,
	})
	if err == nil {
		t.Fatal("unhomed 候補が受理された; 拒否されるべき")
	}
	if !strings.Contains(err.Error(), "unhomed") {
		t.Errorf("エラーが理由を示していない: %v", err)
	}
}
