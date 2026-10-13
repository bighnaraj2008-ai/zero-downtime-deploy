package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

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
		cfg.Tag = ""
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

	return orchestrator.Deploy(ctx)
}

func main() {
	rootCmd := &cobra.Command{
		Use:   "zero-downtime-deploy",
		Short: "Robust Blue/Green zero-downtime container deployment CLI tool",
	}

	rootCmd.PersistentFlags().StringVarP(&configPath, "config", "c", "deploy.yaml", "Path to deployment configuration file")

	deployCmd := &cobra.Command{
		Use:   "deploy",
		Short: "Deploy a new version using zero-downtime Blue/Green orchestration",
		RunE:  runDeploy,
	}
	deployCmd.Flags().StringVarP(&imageFlag, "image", "i", "", "Override container image (e.g., 'myapp:v2.0.0')")
	deployCmd.Flags().BoolVar(&dryRunFlag, "dry-run", false, "Simulate deployment steps without modifying Docker containers")

	versionCmd := &cobra.Command{
		Use:   "version",
		Short: "Display version information",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("zero-downtime-deploy v%s\n", version)
		},
	}

	rootCmd.AddCommand(deployCmd, versionCmd)

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
