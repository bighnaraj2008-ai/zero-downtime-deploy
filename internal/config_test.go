package internal

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfig_Valid(t *testing.T) {
	tmpDir := t.TempDir()
	cfgFile := filepath.Join(tmpDir, "deploy.yaml")

	content := `
app_name: "test-app"
image: "nginx"
tag: "1.25"
container_port: 8080
blue_port: 9001
green_port: 9002
live_port: 9000
health_check:
  path: "/healthz"
  initial_delay_seconds: 1
  interval_seconds: 1
  timeout_seconds: 3
  max_retries: 5
  expected_status: 200
drain_timeout_seconds: 3
rollback_on_failure: true
env:
  STAGE: "test"
`
	if err := os.WriteFile(cfgFile, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write test config: %v", err)
	}

	cfg, err := LoadConfig(cfgFile)
	if err != nil {
		t.Fatalf("expected valid config, got error: %v", err)
	}

	if cfg.AppName != "test-app" {
		t.Errorf("expected AppName 'test-app', got '%s'", cfg.AppName)
	}
	if cfg.FullImageName() != "nginx:1.25" {
		t.Errorf("expected FullImageName 'nginx:1.25', got '%s'", cfg.FullImageName())
	}
	if cfg.BluePort != 9001 || cfg.GreenPort != 9002 || cfg.LivePort != 9000 {
		t.Errorf("unexpected port mapping")
	}
}

func TestLoadConfig_ValidationErrors(t *testing.T) {
	tests := []struct {
		name    string
		content string
		wantErr string
	}{
		{
			name: "missing app_name",
			content: `
image: "nginx"
blue_port: 8081
green_port: 8082
`,
			wantErr: "app_name is required",
		},
		{
			name: "missing image",
			content: `
app_name: "app"
blue_port: 8081
green_port: 8082
`,
			wantErr: "image is required",
		},
		{
			name: "port collision blue and green",
			content: `
app_name: "app"
image: "nginx"
blue_port: 8080
green_port: 8080
`,
			wantErr: "blue_port and green_port must be distinct",
		},
		{
			name: "port collision live and blue",
			content: `
app_name: "app"
image: "nginx"
blue_port: 8080
green_port: 8081
live_port: 8080
`,
			wantErr: "live_port cannot collide with blue_port or green_port",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			file := filepath.Join(tmpDir, "deploy.yaml")
			_ = os.WriteFile(file, []byte(tt.content), 0644)

			_, err := LoadConfig(file)
			if err == nil {
				t.Fatalf("expected error for %s, got nil", tt.name)
			}
			if tt.wantErr != "" && !contains(err.Error(), tt.wantErr) {
				t.Errorf("expected error to contain '%s', got '%v'", tt.wantErr, err)
			}
		})
	}
}

func contains(str, substr string) bool {
	return len(str) >= len(substr) && (str == substr || len(substr) == 0 || (len(str) > 0 && len(substr) > 0 && (filepath.Base(str) != "" && stringContains(str, substr))))
}

func stringContains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
