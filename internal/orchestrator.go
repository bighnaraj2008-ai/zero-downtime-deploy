package internal

import (
	"fmt"
	"time"
)

// Orchestrator coordinates zero-downtime Blue/Green deployments.
type Orchestrator struct {
	config        *Config
	docker        DockerManager
	healthChecker HealthChecker
	state         *DeployState
	logFn         func(format string, args ...any)
}

// StatusReport details current active/standby environments.
type StatusReport struct {
	AppName             string         `json:"app_name"`
	ActiveSlot          string         `json:"active_slot"`
	ActivePort          int            `json:"active_port"`
	ActiveImage         string         `json:"active_image"`
	ActiveContainer     *ContainerInfo `json:"active_container,omitempty"`
	StandbySlot         string         `json:"standby_slot"`
	StandbyPort         int            `json:"standby_port"`
	StandbyImage        string         `json:"standby_image"`
	StandbyContainer    *ContainerInfo `json:"standby_container,omitempty"`
	LastDeployTime      time.Time      `json:"last_deploy_time"`
	HistorySummaryCount int            `json:"history_count"`
}

// NewOrchestrator creates an Orchestrator instance.
func NewOrchestrator(cfg *Config, docker DockerManager, health HealthChecker, logFn func(string, ...any)) (*Orchestrator, error) {
	if logFn == nil {
		logFn = func(format string, args ...any) { fmt.Printf(format+"\n", args...) }
	}

	state, err := LoadState(cfg.StateFile, cfg.AppName)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize state: %w", err)
	}

	return &Orchestrator{
		config:        cfg,
		docker:        docker,
		healthChecker: health,
		state:         state,
		logFn:         logFn,
	}, nil
}
