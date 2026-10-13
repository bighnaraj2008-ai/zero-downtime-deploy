package internal

import (
	"path/filepath"
	"testing"
)

func TestState_LoadNonExistent(t *testing.T) {
	tmpDir := t.TempDir()
	stateFile := filepath.Join(tmpDir, ".non-existent-state.json")

	state, err := LoadState(stateFile, "my-app")
	if err != nil {
		t.Fatalf("unexpected error loading non-existent state: %v", err)
	}

	if state.AppName != "my-app" {
		t.Errorf("expected app name 'my-app', got '%s'", state.AppName)
	}
	if state.ActiveSlot != "" {
		t.Errorf("expected empty active slot on new state")
	}
}

func TestState_SaveAndReload(t *testing.T) {
	tmpDir := t.TempDir()
	stateFile := filepath.Join(tmpDir, ".deploy-state.json")

	state, err := LoadState(stateFile, "demo-app")
	if err != nil {
		t.Fatalf("failed to initialize state: %v", err)
	}

	state.ActiveSlot = "blue"
	state.ActivePort = 8081
	state.ActiveImage = "demo:v1.0"
	state.ActiveContainerID = "cid-blue-123456"
	state.AddHistory("blue", "demo:v1.0", "success", "Initial release")

	if err := state.Save(stateFile); err != nil {
		t.Fatalf("failed to save state: %v", err)
	}

	reloaded, err := LoadState(stateFile, "demo-app")
	if err != nil {
		t.Fatalf("failed to reload state: %v", err)
	}

	if reloaded.ActiveSlot != "blue" {
		t.Errorf("expected ActiveSlot 'blue', got '%s'", reloaded.ActiveSlot)
	}
	if reloaded.ActivePort != 8081 {
		t.Errorf("expected ActivePort 8081, got %d", reloaded.ActivePort)
	}
	if len(reloaded.History) != 1 {
		t.Fatalf("expected 1 history record, got %d", len(reloaded.History))
	}
	if reloaded.History[0].Status != "success" {
		t.Errorf("expected status 'success', got '%s'", reloaded.History[0].Status)
	}
}

func TestState_HistoryCap(t *testing.T) {
	state := &DeployState{
		AppName: "cap-test",
		History: make([]DeployRecord, 0),
	}

	for i := 0; i < 60; i++ {
		state.AddHistory("blue", "image:tag", "success", "iteration")
	}

	if len(state.History) > 50 {
		t.Errorf("expected bounded history at 50 records, got %d", len(state.History))
	}
}
