package internal

import (
	"context"
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

// Deploy executes the zero-downtime deployment workflow.
func (o *Orchestrator) Deploy(ctx context.Context) error {
	cfg := o.config
	targetImage := cfg.FullImageName()

	o.logFn("=================================================================")
	o.logFn("🚀 STARTING ZERO-DOWNTIME BLUE/GREEN DEPLOYMENT")
	o.logFn("   Application:  %s", cfg.AppName)
	o.logFn("   Target Image: %s", targetImage)
	o.logFn("   Live Port:    %d", cfg.LivePort)
	o.logFn("=================================================================")

	// Determine active and candidate target slots
	activeSlot := o.state.ActiveSlot
	var candidateSlot string
	var candidatePort int
	var oldPort int

	if activeSlot == "blue" {
		candidateSlot = "green"
		candidatePort = cfg.GreenPort
		oldPort = cfg.BluePort
	} else if activeSlot == "green" {
		candidateSlot = "blue"
		candidatePort = cfg.BluePort
		oldPort = cfg.GreenPort
	} else {
		candidateSlot = "blue"
		candidatePort = cfg.BluePort
		oldPort = 0
	}

	o.logFn("\n[Phase 1/5] Slot & Environment Detection")
	if activeSlot == "" {
		o.logFn("  • Initial deployment detected: Target slot -> %s (Port: %d)", candidateSlot, candidatePort)
	} else {
		o.logFn("  • Currently Active Slot: %s (Port: %d, Image: %s)", activeSlot, oldPort, o.state.ActiveImage)
		o.logFn("  • Target Candidate Slot: %s (Port: %d, Image: %s)", candidateSlot, candidatePort, targetImage)
	}

	if cfg.DryRun {
		o.logFn("\n🔍 [DRY-RUN] Simulating deployment steps without modifying Docker containers...")
		return nil
	}
	return nil
}
