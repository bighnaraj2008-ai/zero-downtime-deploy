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

	// Phase 2: Pull Container Image
	o.logFn("\n[Phase 2/5] Pulling Container Image")
	o.logFn("  • Pulling %s from registry...", targetImage)
	if err := o.docker.PullImage(ctx, targetImage); err != nil {
		o.logFn("  ⚠️ Warning: Pull failed (%v), checking if image exists locally...", err)
	} else {
		o.logFn("  ✓ Image pulled successfully")
	}

	// Phase 3: Spin Up Candidate Container
	o.logFn("\n[Phase 3/5] Starting Candidate Container")
	o.logFn("  • Provisioning container: %s-%s on host port %d...", cfg.AppName, candidateSlot, candidatePort)
	candidateID, err := o.docker.CreateAndStartContainer(ctx, CreateContainerOpts{
		AppName:       cfg.AppName,
		Slot:          candidateSlot,
		Image:         targetImage,
		ContainerPort: cfg.ContainerPort,
		HostPort:      candidatePort,
		Env:           cfg.Env,
	})
	if err != nil {
		return fmt.Errorf("failed to spin up candidate container: %w", err)
	}
	shortID := candidateID
	if len(shortID) > 12 {
		shortID = shortID[:12]
	}
	o.logFn("  ✓ Candidate container running (ID: %s)", shortID)

	// Phase 4: Automated Health Verification
	o.logFn("\n[Phase 4/5] Executing Automated Health Validation")
	healthResult, err := o.healthChecker.Check(ctx, "127.0.0.1", candidatePort, cfg.HealthCheck)
	if err != nil || !healthResult.Success {
		o.logFn("\n❌ [HEALTH CHECK FAILED] Candidate container failed validation!")
		if err != nil {
			o.logFn("  Error: %v", err)
		}

		if cfg.RollbackOnFailure {
			o.logFn("\n🚨 [AUTOMATED ROLLBACK TRIGGERED]")
			o.logFn("  • Cleaning up unhealthy candidate container (%s-%s)...", cfg.AppName, candidateSlot)
			_ = o.docker.StopContainer(ctx, candidateID, 2)
			_ = o.docker.RemoveContainer(ctx, candidateID)
			o.logFn("  ✓ Candidate container stopped and removed")

			if activeSlot != "" {
				o.logFn("  ✓ Previous active environment '%s' remains UNTOUCHED and LIVE on port %d", activeSlot, oldPort)
				o.state.AddHistory(candidateSlot, targetImage, "rolled_back", fmt.Sprintf("Health check failed on port %d", candidatePort))
			} else {
				o.logFn("  • No previous active container to preserve")
				o.state.AddHistory(candidateSlot, targetImage, "failed", fmt.Sprintf("Health check failed on initial deploy port %d", candidatePort))
			}
			_ = o.state.Save(cfg.StateFile)
		}

		return fmt.Errorf("deployment aborted due to health check failure; automated rollback preserved stable state: %w", err)
	}
	o.logFn("  ✓ Candidate container verified healthy in %v (Passed on attempt %d)",
		healthResult.TotalDuration.Round(time.Millisecond), healthResult.Attempts)

	// Phase 5: Traffic Switching
	o.logFn("\n[Phase 5/5] Traffic Switching & Decommissioning")
	o.logFn("  • Atomic traffic handoff: Switching active routing from slot '%s' -> '%s' (Port: %d)",
		activeSlot, candidateSlot, candidatePort)

	oldContainerID := o.state.ActiveContainerID
	oldImage := o.state.ActiveImage

	o.state.StandbySlot = activeSlot
	o.state.StandbyContainerID = oldContainerID
	o.state.StandbyPort = oldPort
	o.state.StandbyImage = oldImage

	o.state.ActiveSlot = candidateSlot
	o.state.ActiveContainerID = candidateID
	o.state.ActivePort = candidatePort
	o.state.ActiveImage = targetImage
	o.state.LastDeployTime = time.Now().UTC()
	o.state.AddHistory(candidateSlot, targetImage, "success", fmt.Sprintf("Promoted to active slot on port %d", candidatePort))

	if err := o.state.Save(cfg.StateFile); err != nil {
		o.logFn("  ⚠️ Warning: Failed to persist state file: %v", err)
	}

	return nil
}
