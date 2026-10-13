package internal

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/client"
)

// ContainerInfo encapsulates details of an active or standby container.
type ContainerInfo struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Image     string            `json:"image"`
	Slot      string            `json:"slot"`
	HostPort  int               `json:"host_port"`
	IsRunning bool              `json:"is_running"`
	Status    string            `json:"status"`
	CreatedAt time.Time         `json:"created_at"`
	Labels    map[string]string `json:"labels"`
}

// DockerManager defines operations required for container lifecycle management.
type DockerManager interface {
	PullImage(ctx context.Context, imageRef string) error
	CreateAndStartContainer(ctx context.Context, opts CreateContainerOpts) (string, error)
	InspectContainer(ctx context.Context, containerID string) (*ContainerInfo, error)
	StopContainer(ctx context.Context, containerID string, timeoutSeconds int) error
	RemoveContainer(ctx context.Context, containerID string) error
	FindContainerBySlot(ctx context.Context, appName, slot string) (*ContainerInfo, error)
	Ping(ctx context.Context) error
	Close() error
}

// CreateContainerOpts contains options for launching a new deployment slot container.
type CreateContainerOpts struct {
	AppName       string
	Slot          string
	Image         string
	ContainerPort int
	HostPort      int
	Env           map[string]string
}

// RealDockerManager implements DockerManager using the official Docker Go SDK.
type RealDockerManager struct {
	cli *client.Client
}

// NewRealDockerManager initializes a Docker client connected via local socket or environment.
func NewRealDockerManager() (*RealDockerManager, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("failed to create Docker client: %w", err)
	}
	return &RealDockerManager{cli: cli}, nil
}

// Ping checks if the Docker daemon is accessible.
func (d *RealDockerManager) Ping(ctx context.Context) error {
	_, err := d.cli.Ping(ctx)
	if err != nil {
		return fmt.Errorf("docker daemon unavailable: %w", err)
	}
	return nil
}

// Close releases Docker client resources.
func (d *RealDockerManager) Close() error {
	return d.cli.Close()
}

// PullImage pulls the specified Docker image from a registry.
func (d *RealDockerManager) PullImage(ctx context.Context, imageRef string) error {
	reader, err := d.cli.ImagePull(ctx, imageRef, image.PullOptions{})
	if err != nil {
		return fmt.Errorf("failed to pull image '%s': %w", imageRef, err)
	}
	defer reader.Close()

	// Consume the output stream to ensure pull completes
	_, _ = io.Copy(io.Discard, reader)
	return nil
}
