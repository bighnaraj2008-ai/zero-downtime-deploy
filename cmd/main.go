package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"zero-downtime-deploy/internal"
)

var (
	configPath string
	imageFlag  string
	dryRunFlag bool
	portFlag   int
	version    = "1.0.0"
)

func main() {
	rootCmd := &cobra.Command{
		Use:          "zero-downtime-deploy",
		Short:        "Robust Blue/Green zero-downtime container deployment CLI tool",
		SilenceUsage: true,
		Long: `A release engineering orchestration CLI in Go that manages zero-downtime 
Blue/Green deployments for Docker containers with automated health checks, 
traffic switching, and instant automated rollbacks on failure.`,
	}

	rootCmd.PersistentFlags().StringVarP(&configPath, "config", "c", "deploy.yaml", "Path to deployment configuration file")

	deployCmd := &cobra.Command{
		Use:   "deploy",
		Short: "Deploy a new version using zero-downtime Blue/Green orchestration",
		RunE:  runDeploy,
	}
	deployCmd.Flags().StringVarP(&imageFlag, "image", "i", "", "Override container image (e.g., 'myapp:v2.0.0')")
	deployCmd.Flags().BoolVar(&dryRunFlag, "dry-run", false, "Simulate deployment steps without modifying Docker containers")

	rollbackCmd := &cobra.Command{
		Use:   "rollback",
		Short: "Rollback immediately to the previous stable release",
		RunE:  runRollback,
	}

	statusCmd := &cobra.Command{
		Use:   "status",
		Short: "Inspect the current Blue/Green environment status and history",
		RunE:  runStatus,
	}

	routerCmd := &cobra.Command{
		Use:   "router",
		Short: "Start the zero-downtime reverse proxy on the live ingress port",
		RunE:  runRouter,
	}
	routerCmd.Flags().IntVarP(&portFlag, "port", "p", 0, "Override live traffic ingress port")

	versionCmd := &cobra.Command{
		Use:   "version",
		Short: "Display version information",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("zero-downtime-deploy v%s (Go Release Engineering CLI)\n", version)
		},
	}

	rootCmd.AddCommand(deployCmd, rollbackCmd, statusCmd, routerCmd, versionCmd)

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func initContext() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigChan
		fmt.Println("\nReceived interrupt signal, initiating graceful shutdown...")
		cancel()
	}()
	return ctx, cancel
}

func loadConfigAndDocker(dryRun bool) (*internal.Config, internal.DockerManager, error) {
	cfg, err := internal.LoadConfig(configPath)
	if err != nil {
		return nil, nil, err
	}

	if imageFlag != "" {
		cfg.Image = imageFlag
		cfg.Tag = "" // Handled within image string
	}
	if dryRun {
		cfg.DryRun = true
	}

	dockerMgr, err := internal.NewRealDockerManager()
	if err != nil {
		if dryRun {
			return cfg, nil, nil
		}
		return nil, nil, fmt.Errorf("failed to connect to Docker daemon: %w", err)
	}

	return cfg, dockerMgr, nil
}

func runDeploy(cmd *cobra.Command, args []string) error {
	ctx, cancel := initContext()
	defer cancel()

	cfg, dockerMgr, err := loadConfigAndDocker(dryRunFlag)
	if err != nil {
		return err
	}
	if dockerMgr != nil {
		defer dockerMgr.Close()
		// Test Docker daemon connectivity if not dry run
		if !dryRunFlag {
			if err := dockerMgr.Ping(ctx); err != nil {
				return fmt.Errorf("docker daemon is unreachable (%v). Please verify Docker is running", err)
			}
		}
	}

	healthChecker := internal.NewHTTPHealthChecker(func(format string, args ...any) {
		fmt.Printf(format+"\n", args...)
	})

	orchestrator, err := internal.NewOrchestrator(cfg, dockerMgr, healthChecker, func(format string, args ...any) {
		fmt.Printf(format+"\n", args...)
	})
	if err != nil {
		return fmt.Errorf("orchestrator initialization failed: %w", err)
	}

	if err := orchestrator.Deploy(ctx); err != nil {
		return err
	}

	return nil
}

func runRollback(cmd *cobra.Command, args []string) error {
	ctx, cancel := initContext()
	defer cancel()

	cfg, dockerMgr, err := loadConfigAndDocker(false)
	if err != nil {
		return err
	}
	defer dockerMgr.Close()

	if err := dockerMgr.Ping(ctx); err != nil {
		return fmt.Errorf("docker daemon is unreachable (%v). Please verify Docker is running", err)
	}

	healthChecker := internal.NewHTTPHealthChecker(func(format string, args ...any) {
		fmt.Printf(format+"\n", args...)
	})

	orchestrator, err := internal.NewOrchestrator(cfg, dockerMgr, healthChecker, func(format string, args ...any) {
		fmt.Printf(format+"\n", args...)
	})
	if err != nil {
		return fmt.Errorf("orchestrator initialization failed: %w", err)
	}

	if err := orchestrator.Rollback(ctx); err != nil {
		return err
	}

	return nil
}

func runStatus(cmd *cobra.Command, args []string) error {
	ctx, cancel := initContext()
	defer cancel()
	_ = ctx

	cfg, err := internal.LoadConfig(configPath)
	if err != nil {
		return err
	}

	state, err := internal.LoadState(cfg.StateFile, cfg.AppName)
	if err != nil {
		return err
	}

	fmt.Println("=================================================================")
	fmt.Printf("📊 BLUE/GREEN ENVIRONMENT STATUS - %s\n", cfg.AppName)
	fmt.Println("=================================================================")
	if state.ActiveSlot == "" {
		fmt.Println("Status: No active deployment recorded yet.")
		fmt.Printf("Configured Live Port:   %d\n", cfg.LivePort)
		fmt.Printf("Configured Blue Port:   %d\n", cfg.BluePort)
		fmt.Printf("Configured Green Port:  %d\n", cfg.GreenPort)
		fmt.Println("Run 'deploy' to initiate the first deployment.")
		return nil
	}

	fmt.Printf("Active Slot:          %s\n", state.ActiveSlot)
	fmt.Printf("Active Port:          %d\n", state.ActivePort)
	fmt.Printf("Active Image:         %s\n", state.ActiveImage)
	if state.ActiveContainerID != "" {
		cID := state.ActiveContainerID
		if len(cID) > 12 {
			cID = cID[:12]
		}
		fmt.Printf("Active Container ID:  %s\n", cID)
	}
	fmt.Printf("Last Deployed:        %s\n", state.LastDeployTime.Format(time.RFC1123))

	if state.StandbySlot != "" {
		fmt.Println("-----------------------------------------------------------------")
		fmt.Printf("Standby (Stable) Slot: %s\n", state.StandbySlot)
		fmt.Printf("Standby Port:          %d\n", state.StandbyPort)
		fmt.Printf("Standby Image:         %s\n", state.StandbyImage)
	}

	if len(state.History) > 0 {
		fmt.Println("-----------------------------------------------------------------")
		fmt.Println("Recent Deployment Audit Trail:")
		startIdx := 0
		if len(state.History) > 5 {
			startIdx = len(state.History) - 5
		}
		for i := len(state.History) - 1; i >= startIdx; i-- {
			rec := state.History[i]
			fmt.Printf("  • [%s] Slot: %-5s | Status: %-11s | Image: %s | %s\n",
				rec.Timestamp.Format("15:04:05"), rec.Slot, rec.Status, rec.Image, rec.Message)
		}
	}
	fmt.Println("=================================================================")
	return nil
}

func runRouter(cmd *cobra.Command, args []string) error {
	ctx, cancel := initContext()
	defer cancel()

	cfg, err := internal.LoadConfig(configPath)
	if err != nil {
		return err
	}

	state, err := internal.LoadState(cfg.StateFile, cfg.AppName)
	if err != nil {
		return err
	}

	livePort := cfg.LivePort
	if portFlag > 0 {
		livePort = portFlag
	}

	router := internal.NewTrafficRouter(livePort, cfg.StateFile, func(format string, args ...any) {
		fmt.Printf(format+"\n", args...)
	})

	if state.ActiveSlot != "" && state.ActivePort > 0 {
		if err := router.SetUpstream(state.ActiveSlot, state.ActivePort); err != nil {
			return err
		}
	} else {
		fmt.Println("Note: Starting router with no active upstream yet. Run 'deploy' to direct traffic.")
	}

	// Poll state file in background to automatically reload active upstream changes
	go func() {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()
		lastSlot := state.ActiveSlot
		lastPort := state.ActivePort

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				st, err := internal.LoadState(cfg.StateFile, cfg.AppName)
				if err == nil && st.ActiveSlot != "" && (st.ActiveSlot != lastSlot || st.ActivePort != lastPort) {
					lastSlot = st.ActiveSlot
					lastPort = st.ActivePort
					_ = router.SetUpstream(st.ActiveSlot, st.ActivePort)
				}
			}
		}
	}()

	go func() {
		<-ctx.Done()
		shutdownCtx, sCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer sCancel()
		_ = router.Shutdown(shutdownCtx)
	}()

	addr := fmt.Sprintf(":%d", livePort)
	if err := router.Start(addr); err != nil && err != context.Canceled {
		return err
	}
	return nil
}
