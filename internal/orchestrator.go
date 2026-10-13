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
	AppName            string         `json:"app_name"`
	ActiveSlot         string         `json:"active_slot"`
	ActivePort         int            `json:"active_port"`
	ActiveImage        string         `json:"active_image"`
	ActiveContainer    *ContainerInfo `json:"active_container,omitempty"`
	StandbySlot        string         `json:"standby_slot"`
	StandbyPort        int            `json:"standby_port"`
	StandbyImage       string         `json:"standby_image"`
	StandbyContainer   *ContainerInfo `json:"standby_container,omitempty"`
	LastDeployTime     time.Time      `json:"last_deploy_time"`
	HistorySummaryCount int           `json:"history_count"`
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
		// First-time deployment: Default to blue
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
		o.logFn("  • Pull image '%s' -> (dry-run OK)", targetImage)
		o.logFn("  • Create container '%s-%s' on port %d -> (dry-run OK)", cfg.AppName, candidateSlot, candidatePort)
		o.logFn("  • Health check candidate on :%d%s -> (dry-run OK)", candidatePort, cfg.HealthCheck.Path)
		o.logFn("  • Switch live traffic to slot '%s' -> (dry-run OK)", candidateSlot)
		o.logFn("  • Drain old slot '%s' for %ds -> (dry-run OK)", activeSlot, cfg.DrainTimeoutSeconds)
		o.logFn("✨ Dry-run validation completed successfully.")
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

	// Phase 5: Traffic Switching & Connection Draining
	o.logFn("\n[Phase 5/5] Traffic Switching & Decommissioning")
	o.logFn("  • Atomic traffic handoff: Switching active routing from slot '%s' -> '%s' (Port: %d)",
		activeSlot, candidateSlot, candidatePort)

	oldContainerID := o.state.ActiveContainerID
	oldImage := o.state.ActiveImage

	// Update state to make candidate the new active release
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
	} else {
		o.logFn("  ✓ Deployment state saved successfully to '%s'", cfg.StateFile)
	}

	// Drain in-flight connections on old container
	if oldContainerID != "" && activeSlot != "" {
		drainSeconds := cfg.DrainTimeoutSeconds
		if drainSeconds > 0 {
			o.logFn("  • Draining in-flight connections on old container (%s-%s) for %ds...",
				cfg.AppName, activeSlot, drainSeconds)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(drainSeconds) * time.Second):
			}
		}

		o.logFn("  • Gracefully stopping old container (%s-%s)...", cfg.AppName, activeSlot)
		if err := o.docker.StopContainer(ctx, oldContainerID, 5); err != nil {
			o.logFn("  ⚠️ Could not stop old container: %v", err)
		} else {
			o.logFn("  ✓ Old container decommissioned gracefully")
		}
	}

	o.logFn("\n=================================================================")
	o.logFn("🎉 ZERO-DOWNTIME DEPLOYMENT SUCCEEDED!")
	o.logFn("   Active Environment: %s", candidateSlot)
	o.logFn("   Active Host Port:   %d", candidatePort)
	o.logFn("   Deployed Image:     %s", targetImage)
	o.logFn("=================================================================")
	return nil
}

// Rollback restores the previous stable environment immediately.
func (o *Orchestrator) Rollback(ctx context.Context) error {
	cfg := o.config
	o.logFn("=================================================================")
	o.logFn("⏪ INITIATING INSTANT ROLLBACK")
	o.logFn("   Application: %s", cfg.AppName)
	o.logFn("=================================================================")

	activeSlot := o.state.ActiveSlot
	standbySlot := o.state.StandbySlot
	standbyImage := o.state.StandbyImage
	standbyPort := o.state.StandbyPort
	standbyContainerID := o.state.StandbyContainerID

	if standbySlot == "" || standbyImage == "" {
		return fmt.Errorf("rollback aborted: no previous stable deployment recorded in state")
	}

	o.logFn("  • Current Active Slot:  %s (Port: %d, Image: %s)", activeSlot, o.state.ActivePort, o.state.ActiveImage)
	o.logFn("  • Target Rollback Slot: %s (Port: %d, Image: %s)", standbySlot, standbyPort, standbyImage)

	// Verify if the previous standby container is still present
	needNewContainer := false
	if standbyContainerID != "" {
		inspect, err := o.docker.InspectContainer(ctx, standbyContainerID)
		if err != nil || inspect == nil {
			needNewContainer = true
		} else if !inspect.IsRunning {
			o.logFn("  • Standby container is stopped, restarting...")
			// Create clean replacement on standby slot
			needNewContainer = true
		}
	} else {
		needNewContainer = true
	}

	var rollbackContainerID string
	if needNewContainer {
		o.logFn("  • Re-spawning previous stable container (%s-%s)...", cfg.AppName, standbySlot)
		cID, err := o.docker.CreateAndStartContainer(ctx, CreateContainerOpts{
			AppName:       cfg.AppName,
			Slot:          standbySlot,
			Image:         standbyImage,
			ContainerPort: cfg.ContainerPort,
			HostPort:      standbyPort,
			Env:           cfg.Env,
		})
		if err != nil {
			return fmt.Errorf("failed to recreate previous stable container: %w", err)
		}
		rollbackContainerID = cID
	} else {
		rollbackContainerID = standbyContainerID
	}

	// Verify health of the rollback candidate
	o.logFn("  • Validating health of rollback container on port %d...", standbyPort)
	healthResult, err := o.healthChecker.Check(ctx, "127.0.0.1", standbyPort, cfg.HealthCheck)
	if err != nil || !healthResult.Success {
		return fmt.Errorf("rollback aborted: rollback target failed health check: %w", err)
	}
	o.logFn("  ✓ Rollback target verified healthy")

	// Switch traffic back
	o.logFn("  • Reverting active traffic back to slot '%s' (Port: %d)", standbySlot, standbyPort)
	oldActiveContainerID := o.state.ActiveContainerID
	oldActiveSlot := o.state.ActiveSlot
	oldActivePort := o.state.ActivePort
	oldActiveImage := o.state.ActiveImage

	o.state.ActiveSlot = standbySlot
	o.state.ActiveContainerID = rollbackContainerID
	o.state.ActivePort = standbyPort
	o.state.ActiveImage = standbyImage

	o.state.StandbySlot = oldActiveSlot
	o.state.StandbyContainerID = oldActiveContainerID
	o.state.StandbyPort = oldActivePort
	o.state.StandbyImage = oldActiveImage

	o.state.LastDeployTime = time.Now().UTC()
	o.state.AddHistory(standbySlot, standbyImage, "rolled_back", fmt.Sprintf("Rolled back to %s on port %d", standbyImage, standbyPort))
	_ = o.state.Save(cfg.StateFile)

	// Stop the faulty container
	if oldActiveContainerID != "" {
		o.logFn("  • Decommissioning faulty release container (%s-%s)...", cfg.AppName, oldActiveSlot)
		_ = o.docker.StopContainer(ctx, oldActiveContainerID, 3)
	}

	o.logFn("\n=================================================================")
	o.logFn("✓ ROLLBACK COMPLETED SUCCESSFULLY")
	o.logFn("   Active Environment: %s", standbySlot)
	o.logFn("   Active Host Port:   %d", standbyPort)
	o.logFn("   Stable Image:       %s", standbyImage)
	o.logFn("=================================================================")
	return nil
}

// GetStatus returns the current status report of both Blue and Green slots.
func (o *Orchestrator) GetStatus(ctx context.Context) (*StatusReport, error) {
	report := &StatusReport{
		AppName:             o.config.AppName,
		ActiveSlot:          o.state.ActiveSlot,
		ActivePort:          o.state.ActivePort,
		ActiveImage:         o.state.ActiveImage,
		StandbySlot:         o.state.StandbySlot,
		StandbyPort:         o.state.StandbyPort,
		StandbyImage:        o.state.StandbyImage,
		LastDeployTime:      o.state.LastDeployTime,
		HistorySummaryCount: len(o.state.History),
	}

	if o.state.ActiveContainerID != "" {
		if cInfo, err := o.docker.InspectContainer(ctx, o.state.ActiveContainerID); err == nil {
			report.ActiveContainer = cInfo
		}
	}

	if o.state.StandbyContainerID != "" {
		if cInfo, err := o.docker.InspectContainer(ctx, o.state.StandbyContainerID); err == nil {
			report.StandbyContainer = cInfo
		}
	}

	return report, nil
}
