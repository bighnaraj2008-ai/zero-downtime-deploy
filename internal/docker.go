package internal

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
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
	Slot          string // "blue" or "green"
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

// CreateAndStartContainer creates a container for a slot and maps its host port.
func (d *RealDockerManager) CreateAndStartContainer(ctx context.Context, opts CreateContainerOpts) (string, error) {
	containerName := fmt.Sprintf("%s-%s", opts.AppName, opts.Slot)

	// Clean up any stale container with the same name before creation
	existing, _ := d.findContainerByName(ctx, containerName)
	if existing != nil {
		_ = d.StopContainer(ctx, existing.ID, 3)
		_ = d.RemoveContainer(ctx, existing.ID)
	}

	cPortStr := fmt.Sprintf("%d/tcp", opts.ContainerPort)
	cPort, err := nat.NewPort("tcp", strconv.Itoa(opts.ContainerPort))
	if err != nil {
		return "", fmt.Errorf("invalid container port %d: %w", opts.ContainerPort, err)
	}

	portBindings := nat.PortMap{
		cPort: []nat.PortBinding{
			{
				HostIP:   "0.0.0.0",
				HostPort: strconv.Itoa(opts.HostPort),
			},
		},
	}

	// Build environment variable slice
	envList := make([]string, 0, len(opts.Env)+2)
	envList = append(envList, fmt.Sprintf("DEPLOY_SLOT=%s", opts.Slot))
	envList = append(envList, fmt.Sprintf("APP_NAME=%s", opts.AppName))
	for k, v := range opts.Env {
		envList = append(envList, fmt.Sprintf("%s=%s", k, v))
	}

	labels := map[string]string{
		"app":         opts.AppName,
		"slot":        opts.Slot,
		"managed_by":  "zero-downtime-deploy",
		"deployed_at": time.Now().UTC().Format(time.RFC3339),
	}

	config := &container.Config{
		Image:        opts.Image,
		ExposedPorts: nat.PortSet{nat.Port(cPortStr): struct{}{}},
		Env:          envList,
		Labels:       labels,
	}

	hostConfig := &container.HostConfig{
		PortBindings: portBindings,
		RestartPolicy: container.RestartPolicy{
			Name: container.RestartPolicyUnlessStopped,
		},
	}

	resp, err := d.cli.ContainerCreate(ctx, config, hostConfig, nil, nil, containerName)
	if err != nil {
		return "", fmt.Errorf("failed to create container '%s': %w", containerName, err)
	}

	if err := d.cli.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		// Clean up container if startup fails
		_ = d.cli.ContainerRemove(ctx, resp.ID, container.RemoveOptions{Force: true})
		return "", fmt.Errorf("failed to start container '%s' (%s): %w", containerName, resp.ID[:12], err)
	}

	return resp.ID, nil
}

// InspectContainer inspects a container and returns detailed runtime metadata.
func (d *RealDockerManager) InspectContainer(ctx context.Context, containerID string) (*ContainerInfo, error) {
	inspect, err := d.cli.ContainerInspect(ctx, containerID)
	if err != nil {
		return nil, fmt.Errorf("failed to inspect container '%s': %w", containerID, err)
	}

	createdAt, _ := time.Parse(time.RFC3339Nano, inspect.Created)
	name := strings.TrimPrefix(inspect.Name, "/")
	slot := inspect.Config.Labels["slot"]

	var hostPort int
	for _, bindings := range inspect.NetworkSettings.Ports {
		if len(bindings) > 0 {
			if p, err := strconv.Atoi(bindings[0].HostPort); err == nil {
				hostPort = p
				break
			}
		}
	}

	return &ContainerInfo{
		ID:        inspect.ID,
		Name:      name,
		Image:     inspect.Config.Image,
		Slot:      slot,
		HostPort:  hostPort,
		IsRunning: inspect.State != nil && inspect.State.Running,
		Status:    inspect.State.Status,
		CreatedAt: createdAt,
		Labels:    inspect.Config.Labels,
	}, nil
}

// StopContainer gracefully stops a container with a specified timeout before SIGKILL.
func (d *RealDockerManager) StopContainer(ctx context.Context, containerID string, timeoutSeconds int) error {
	stopOpts := container.StopOptions{
		Timeout: &timeoutSeconds,
	}
	if err := d.cli.ContainerStop(ctx, containerID, stopOpts); err != nil {
		return fmt.Errorf("failed to stop container '%s': %w", containerID, err)
	}
	return nil
}

// RemoveContainer forces the removal of a container.
func (d *RealDockerManager) RemoveContainer(ctx context.Context, containerID string) error {
	removeOpts := container.RemoveOptions{
		Force:         true,
		RemoveVolumes: true,
	}
	if err := d.cli.ContainerRemove(ctx, containerID, removeOpts); err != nil {
		return fmt.Errorf("failed to remove container '%s': %w", containerID, err)
	}
	return nil
}

// FindContainerBySlot looks up a running container matching the app name and slot labels.
func (d *RealDockerManager) FindContainerBySlot(ctx context.Context, appName, slot string) (*ContainerInfo, error) {
	filterArgs := filters.NewArgs()
	filterArgs.Add("label", fmt.Sprintf("app=%s", appName))
	filterArgs.Add("label", fmt.Sprintf("slot=%s", slot))

	containers, err := d.cli.ContainerList(ctx, container.ListOptions{
		Filters: filterArgs,
		All:     true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list containers: %w", err)
	}

	if len(containers) == 0 {
		return nil, nil
	}

	return d.InspectContainer(ctx, containers[0].ID)
}

func (d *RealDockerManager) findContainerByName(ctx context.Context, name string) (*ContainerInfo, error) {
	containers, err := d.cli.ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		return nil, err
	}
	for _, c := range containers {
		for _, n := range c.Names {
			if strings.TrimPrefix(n, "/") == name {
				return d.InspectContainer(ctx, c.ID)
			}
		}
	}
	return nil, nil
}
