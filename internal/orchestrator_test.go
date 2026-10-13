package internal

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// MockDockerManager implements DockerManager in-memory for testing.
type MockDockerManager struct {
	mu         sync.Mutex
	containers map[string]*ContainerInfo
	pulled     map[string]bool
	stopped    map[string]bool
	removed    map[string]bool
	idCounter  int
}

func NewMockDockerManager() *MockDockerManager {
	return &MockDockerManager{
		containers: make(map[string]*ContainerInfo),
		pulled:     make(map[string]bool),
		stopped:    make(map[string]bool),
		removed:    make(map[string]bool),
	}
}

func (m *MockDockerManager) Ping(ctx context.Context) error {
	return nil
}

func (m *MockDockerManager) Close() error {
	return nil
}

func (m *MockDockerManager) PullImage(ctx context.Context, imageRef string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pulled[imageRef] = true
	return nil
}

func (m *MockDockerManager) CreateAndStartContainer(ctx context.Context, opts CreateContainerOpts) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.idCounter++
	id := fmt.Sprintf("mock-cid-%s-%d", opts.Slot, m.idCounter)

	info := &ContainerInfo{
		ID:        id,
		Name:      fmt.Sprintf("%s-%s", opts.AppName, opts.Slot),
		Image:     opts.Image,
		Slot:      opts.Slot,
		HostPort:  opts.HostPort,
		IsRunning: true,
		Status:    "running",
		CreatedAt: time.Now(),
		Labels: map[string]string{
			"app":  opts.AppName,
			"slot": opts.Slot,
		},
	}
	m.containers[id] = info
	return id, nil
}

func (m *MockDockerManager) InspectContainer(ctx context.Context, containerID string) (*ContainerInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.containers[containerID]
	if !ok {
		return nil, fmt.Errorf("container not found: %s", containerID)
	}
	return c, nil
}

func (m *MockDockerManager) StopContainer(ctx context.Context, containerID string, timeoutSeconds int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.containers[containerID]
	if !ok {
		return fmt.Errorf("container not found: %s", containerID)
	}
	c.IsRunning = false
	c.Status = "exited"
	m.stopped[containerID] = true
	return nil
}

func (m *MockDockerManager) RemoveContainer(ctx context.Context, containerID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.containers, containerID)
	m.removed[containerID] = true
	return nil
}

func (m *MockDockerManager) FindContainerBySlot(ctx context.Context, appName, slot string) (*ContainerInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, c := range m.containers {
		if c.Labels["app"] == appName && c.Slot == slot && c.IsRunning {
			return c, nil
		}
	}
	return nil, nil
}

// MockHealthChecker enables test control over health check pass/fail responses.
type MockHealthChecker struct {
	ShouldPass bool
}

func (m *MockHealthChecker) Check(ctx context.Context, host string, port int, config HealthConfig) (*HealthCheckResult, error) {
	if m.ShouldPass {
		return &HealthCheckResult{
			Success:        true,
			Attempts:       1,
			TotalDuration:  10 * time.Millisecond,
			LastStatusCode: 200,
		}, nil
	}
	return &HealthCheckResult{
		Success:        false,
		Attempts:       config.MaxRetries,
		TotalDuration:  50 * time.Millisecond,
		LastStatusCode: 500,
		LastError:      fmt.Errorf("simulated unhealthy container"),
	}, fmt.Errorf("simulated unhealthy container on port %d", port)
}

func TestOrchestrator_BlueGreenCycle(t *testing.T) {
	tmpDir := t.TempDir()
	statePath := filepath.Join(tmpDir, ".state.json")

	cfg := &Config{
		AppName:             "cycle-test",
		Image:               "myapp",
		Tag:                 "v1.0.0",
		ContainerPort:       80,
		BluePort:            8081,
		GreenPort:           8082,
		LivePort:            8080,
		StateFile:           statePath,
		DrainTimeoutSeconds: 0,
		RollbackOnFailure:   true,
		HealthCheck: HealthConfig{
			Path:       "/healthz",
			MaxRetries: 2,
		},
	}

	docker := NewMockDockerManager()
	health := &MockHealthChecker{ShouldPass: true}

	orch, err := NewOrchestrator(cfg, docker, health, func(string, ...any) {})
	if err != nil {
		t.Fatalf("failed to init orchestrator: %v", err)
	}

	// 1. First deployment: Should land on BLUE
	if err := orch.Deploy(context.Background()); err != nil {
		t.Fatalf("first deployment failed: %v", err)
	}
	if orch.state.ActiveSlot != "blue" {
		t.Errorf("expected active slot 'blue', got '%s'", orch.state.ActiveSlot)
	}
	if orch.state.ActivePort != 8081 {
		t.Errorf("expected active port 8081, got %d", orch.state.ActivePort)
	}

	// 2. Second deployment with new version: Should land on GREEN
	cfg.Tag = "v2.0.0"
	orch2, _ := NewOrchestrator(cfg, docker, health, func(string, ...any) {})
	if err := orch2.Deploy(context.Background()); err != nil {
		t.Fatalf("second deployment failed: %v", err)
	}
	if orch2.state.ActiveSlot != "green" {
		t.Errorf("expected active slot 'green', got '%s'", orch2.state.ActiveSlot)
	}
	if orch2.state.ActivePort != 8082 {
		t.Errorf("expected active port 8082, got %d", orch2.state.ActivePort)
	}
	if orch2.state.StandbySlot != "blue" {
		t.Errorf("expected standby slot 'blue', got '%s'", orch2.state.StandbySlot)
	}

	// 3. Third deployment: Should land back on BLUE
	cfg.Tag = "v3.0.0"
	orch3, _ := NewOrchestrator(cfg, docker, health, func(string, ...any) {})
	if err := orch3.Deploy(context.Background()); err != nil {
		t.Fatalf("third deployment failed: %v", err)
	}
	if orch3.state.ActiveSlot != "blue" {
		t.Errorf("expected active slot 'blue', got '%s'", orch3.state.ActiveSlot)
	}
	if orch3.state.ActivePort != 8081 {
		t.Errorf("expected active port 8081, got %d", orch3.state.ActivePort)
	}
}

func TestOrchestrator_AutomatedRollbackOnFailure(t *testing.T) {
	tmpDir := t.TempDir()
	statePath := filepath.Join(tmpDir, ".state.json")

	cfg := &Config{
		AppName:             "rollback-test",
		Image:               "myapp",
		Tag:                 "v1.0.0",
		ContainerPort:       80,
		BluePort:            8081,
		GreenPort:           8082,
		LivePort:            8080,
		StateFile:           statePath,
		DrainTimeoutSeconds: 0,
		RollbackOnFailure:   true,
		HealthCheck: HealthConfig{
			Path:       "/healthz",
			MaxRetries: 2,
		},
	}

	docker := NewMockDockerManager()
	health := &MockHealthChecker{ShouldPass: true}

	// Step 1: Successful initial deployment to Blue
	orch, _ := NewOrchestrator(cfg, docker, health, func(string, ...any) {})
	if err := orch.Deploy(context.Background()); err != nil {
		t.Fatalf("initial deployment failed: %v", err)
	}
	initialBlueID := orch.state.ActiveContainerID

	// Step 2: Attempt deployment of faulty v2 to Green with failing health checks
	cfg.Tag = "v2.0.0-broken"
	health.ShouldPass = false // SIMULATE FAILING HEALTH CHECK

	orchFailing, _ := NewOrchestrator(cfg, docker, health, func(string, ...any) {})
	err := orchFailing.Deploy(context.Background())
	if err == nil {
		t.Fatalf("expected deployment to fail and trigger rollback, but got nil")
	}

	// Verify that automated rollback kept Blue active and untouched
	reloadedState, _ := LoadState(statePath, cfg.AppName)
	if reloadedState.ActiveSlot != "blue" {
		t.Errorf("expected active slot to remain 'blue', got '%s'", reloadedState.ActiveSlot)
	}
	if reloadedState.ActiveContainerID != initialBlueID {
		t.Errorf("expected initial active container ID to be preserved, got '%s'", reloadedState.ActiveContainerID)
	}

	// Verify that faulty candidate container was cleaned up
	cList := docker.containers
	for _, c := range cList {
		if c.Slot == "green" && c.IsRunning {
			t.Errorf("expected green candidate container to be stopped, but found running: %s", c.ID)
		}
	}

	// Verify audit history records rollback event
	history := reloadedState.History
	if len(history) < 2 {
		t.Fatalf("expected at least 2 history records, got %d", len(history))
	}
	lastRec := history[len(history)-1]
	if lastRec.Status != "rolled_back" {
		t.Errorf("expected last history status 'rolled_back', got '%s'", lastRec.Status)
	}
}

func TestOrchestrator_ManualRollback(t *testing.T) {
	tmpDir := t.TempDir()
	statePath := filepath.Join(tmpDir, ".state.json")

	cfg := &Config{
		AppName:             "manual-rb-test",
		Image:               "myapp",
		Tag:                 "v1.0.0",
		ContainerPort:       80,
		BluePort:            8081,
		GreenPort:           8082,
		LivePort:            8080,
		StateFile:           statePath,
		DrainTimeoutSeconds: 0,
		RollbackOnFailure:   true,
	}

	docker := NewMockDockerManager()
	health := &MockHealthChecker{ShouldPass: true}

	// Deploy v1 on Blue
	orch1, _ := NewOrchestrator(cfg, docker, health, func(string, ...any) {})
	_ = orch1.Deploy(context.Background())

	// Deploy v2 on Green
	cfg.Tag = "v2.0.0"
	orch2, _ := NewOrchestrator(cfg, docker, health, func(string, ...any) {})
	_ = orch2.Deploy(context.Background())

	if orch2.state.ActiveSlot != "green" {
		t.Fatalf("expected active slot 'green'")
	}

	// Trigger Manual Rollback
	orchRollback, _ := NewOrchestrator(cfg, docker, health, func(string, ...any) {})
	if err := orchRollback.Rollback(context.Background()); err != nil {
		t.Fatalf("manual rollback failed: %v", err)
	}

	if orchRollback.state.ActiveSlot != "blue" {
		t.Errorf("expected active slot after rollback to be 'blue', got '%s'", orchRollback.state.ActiveSlot)
	}
	if orchRollback.state.ActivePort != 8081 {
		t.Errorf("expected active port after rollback to be 8081, got %d", orchRollback.state.ActivePort)
	}
}
