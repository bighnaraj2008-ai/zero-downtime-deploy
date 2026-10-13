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

func main() {
	rootCmd := &cobra.Command{
		Use:   "zero-downtime-deploy",
		Short: "Robust Blue/Green zero-downtime container deployment CLI tool",
	}

	rootCmd.PersistentFlags().StringVarP(&configPath, "config", "c", "deploy.yaml", "Path to deployment configuration file")

	versionCmd := &cobra.Command{
		Use:   "version",
		Short: "Display version information",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("zero-downtime-deploy v%s\n", version)
		},
	}

	rootCmd.AddCommand(versionCmd)

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
