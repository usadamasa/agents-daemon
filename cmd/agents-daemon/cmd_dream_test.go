package main

import (
	"slices"
	"testing"
)

func TestDreamCmd_HasFivePhases(t *testing.T) {
	cmd := newDreamCmd()
	var names []string
	for _, sub := range cmd.Commands() {
		names = append(names, sub.Name())
	}
	slices.Sort(names)
	want := []string{"adopt", "forget", "harvest", "mine", "stage"}
	if !slices.Equal(names, want) {
		t.Errorf("subcommands = %v; want %v", names, want)
	}
}

func TestDreamCmd_IsRegisteredOnRoot(t *testing.T) {
	root := newRootCmd()
	sub, _, err := root.Find([]string{"dream", "harvest"})
	if err != nil {
		t.Fatalf("Find(dream harvest): %v", err)
	}
	if sub.Name() != "harvest" {
		t.Errorf("Find(dream harvest) = %q", sub.Name())
	}
}

func TestDreamStageCmd_HasOutFlagInsteadOfExplorationsDir(t *testing.T) {
	cmd := newDreamCmd()
	stage, _, err := cmd.Find([]string{"stage"})
	if err != nil {
		t.Fatal(err)
	}
	if stage.Flags().Lookup("out") == nil {
		t.Error("stage に --out が無い")
	}
	if stage.Flags().Lookup("explorations-dir") != nil {
		t.Error("stage に --explorations-dir が残っている")
	}
}
