package internal

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
)

// DeployRecord represents an entry in the deployment audit log.
type DeployRecord struct {
	Timestamp time.Time `json:"timestamp"`
	Slot      string    `json:"slot"`
	Image     string    `json:"image"`
	Status    string    `json:"status"` // "success", "rolled_back", "failed"
	Message   string    `json:"message"`
}

// DeployState tracks the active and standby environments and history.
type DeployState struct {
	mu                 sync.RWMutex   `json:"-"`
	AppName            string         `json:"app_name"`
	ActiveSlot         string         `json:"active_slot"` // "blue" or "green"
	ActiveContainerID  string         `json:"active_container_id"`
	ActivePort         int            `json:"active_port"`
	ActiveImage        string         `json:"active_image"`
	StandbySlot        string         `json:"standby_slot"`
	StandbyContainerID string         `json:"standby_container_id"`
	StandbyPort        int            `json:"standby_port"`
	StandbyImage       string         `json:"standby_image"`
	LastDeployTime     time.Time      `json:"last_deploy_time"`
	History            []DeployRecord `json:"history,omitempty"`
}

// LoadState reads the state from a JSON file, or returns a fresh state if the file doesn't exist.
func LoadState(filePath, appName string) (*DeployState, error) {
	if filePath == "" {
		filePath = ".deploy-state.json"
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return &DeployState{
				AppName: appName,
				History: make([]DeployRecord, 0),
			}, nil
		}
		return nil, fmt.Errorf("failed to read state file '%s': %w", filePath, err)
	}

	var state DeployState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("failed to parse state file '%s': %w", filePath, err)
	}
	if state.History == nil {
		state.History = make([]DeployRecord, 0)
	}
	return &state, nil
}

// Save writes the state to a JSON file atomically.
func (s *DeployState) Save(filePath string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if filePath == "" {
		filePath = ".deploy-state.json"
	}

	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to serialize state: %w", err)
	}

	tmpFile := filePath + ".tmp"
	if err := os.WriteFile(tmpFile, data, 0644); err != nil {
		return fmt.Errorf("failed to write temp state file: %w", err)
	}

	if err := os.Rename(tmpFile, filePath); err != nil {
		// On Windows, rename might fail if destination exists, so try remove then rename
		_ = os.Remove(filePath)
		if err := os.Rename(tmpFile, filePath); err != nil {
			return fmt.Errorf("failed to commit state file: %w", err)
		}
	}

	return nil
}

// AddHistory appends a new deployment record to state history.
func (s *DeployState) AddHistory(slot, image, status, message string) {
	s.History = append(s.History, DeployRecord{
		Timestamp: time.Now().UTC(),
		Slot:      slot,
		Image:     image,
		Status:    status,
		Message:   message,
	})
	// Keep history bounded to last 50 entries
	if len(s.History) > 50 {
		s.History = s.History[len(s.History)-50:]
	}
}
