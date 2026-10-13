package internal

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// HealthConfig defines the health check polling parameters.
type HealthConfig struct {
	Path                string `yaml:"path" json:"path"`
	InitialDelaySeconds int    `yaml:"initial_delay_seconds" json:"initial_delay_seconds"`
	IntervalSeconds     int    `yaml:"interval_seconds" json:"interval_seconds"`
	TimeoutSeconds      int    `yaml:"timeout_seconds" json:"timeout_seconds"`
	MaxRetries          int    `yaml:"max_retries" json:"max_retries"`
	ExpectedStatus      int    `yaml:"expected_status" json:"expected_status"`
}

// Config defines the complete deployment configuration schema.
type Config struct {
	AppName             string            `yaml:"app_name" json:"app_name"`
	Image               string            `yaml:"image" json:"image"`
	Tag                 string            `yaml:"tag" json:"tag"`
	ContainerPort       int               `yaml:"container_port" json:"container_port"`
	BluePort            int               `yaml:"blue_port" json:"blue_port"`
	GreenPort           int               `yaml:"green_port" json:"green_port"`
	LivePort            int               `yaml:"live_port" json:"live_port"`
	HealthCheck         HealthConfig      `yaml:"health_check" json:"health_check"`
	DrainTimeoutSeconds int               `yaml:"drain_timeout_seconds" json:"drain_timeout_seconds"`
	RollbackOnFailure   bool              `yaml:"rollback_on_failure" json:"rollback_on_failure"`
	Env                 map[string]string `yaml:"env" json:"env"`
	StateFile           string            `yaml:"state_file" json:"state_file"`
	DryRun              bool              `yaml:"dry_run" json:"dry_run"`
}

// FullImageName returns image:tag.
func (c *Config) FullImageName() string {
	if c.Tag == "" {
		return c.Image
	}
	return fmt.Sprintf("%s:%s", c.Image, c.Tag)
}

// LoadConfig reads and parses a YAML configuration file.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file '%s': %w", path, err)
	}

	cfg := &Config{
		ContainerPort:       80,
		BluePort:            8081,
		GreenPort:           8082,
		LivePort:            8080,
		DrainTimeoutSeconds: 5,
		RollbackOnFailure:   true,
		StateFile:           ".deploy-state.json",
		HealthCheck: HealthConfig{
			Path:                "/",
			InitialDelaySeconds: 2,
			IntervalSeconds:     2,
			TimeoutSeconds:      5,
			MaxRetries:          10,
			ExpectedStatus:      200,
		},
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("failed to parse yaml config: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("config validation error: %w", err)
	}

	return cfg, nil
}

// Validate checks that required fields are present and valid.
func (c *Config) Validate() error {
	if c.AppName == "" {
		return fmt.Errorf("app_name is required")
	}
	if c.Image == "" {
		return fmt.Errorf("image is required")
	}
	if c.Tag == "" {
		c.Tag = "latest"
	}
	if c.ContainerPort <= 0 || c.ContainerPort > 65535 {
		return fmt.Errorf("container_port must be between 1 and 65535")
	}
	if c.BluePort <= 0 || c.BluePort > 65535 {
		return fmt.Errorf("blue_port must be between 1 and 65535")
	}
	if c.GreenPort <= 0 || c.GreenPort > 65535 {
		return fmt.Errorf("green_port must be between 1 and 65535")
	}
	if c.BluePort == c.GreenPort {
		return fmt.Errorf("blue_port and green_port must be distinct")
	}
	if c.LivePort > 0 && (c.LivePort == c.BluePort || c.LivePort == c.GreenPort) {
		return fmt.Errorf("live_port cannot collide with blue_port or green_port")
	}
	if c.HealthCheck.Path == "" {
		c.HealthCheck.Path = "/"
	}
	if c.HealthCheck.ExpectedStatus == 0 {
		c.HealthCheck.ExpectedStatus = 200
	}
	if c.HealthCheck.MaxRetries <= 0 {
		c.HealthCheck.MaxRetries = 5
	}
	if c.HealthCheck.IntervalSeconds <= 0 {
		c.HealthCheck.IntervalSeconds = 2
	}
	if c.HealthCheck.TimeoutSeconds <= 0 {
		c.HealthCheck.TimeoutSeconds = 5
	}
	if c.StateFile == "" {
		c.StateFile = ".deploy-state.json"
	}
	return nil
}
