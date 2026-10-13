package internal

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

// HealthCheckResult contains diagnostic details from a health check session.
type HealthCheckResult struct {
	Success        bool          `json:"success"`
	Attempts       int           `json:"attempts"`
	TotalDuration  time.Duration `json:"total_duration"`
	LastStatusCode int           `json:"last_status_code"`
	LastError      error         `json:"last_error,omitempty"`
}

// HealthChecker defines the interface for probing candidate container health.
type HealthChecker interface {
	Check(ctx context.Context, host string, port int, config HealthConfig) (*HealthCheckResult, error)
}

// HTTPHealthChecker performs HTTP GET polling against target containers.
type HTTPHealthChecker struct {
	client *http.Client
	logFn  func(format string, args ...any)
}

// NewHTTPHealthChecker creates a health checker with an optional logging callback.
func NewHTTPHealthChecker(logFn func(format string, args ...any)) *HTTPHealthChecker {
	if logFn == nil {
		logFn = func(format string, args ...any) {
			fmt.Printf(format+"\n", args...)
		}
	}
	return &HTTPHealthChecker{
		client: &http.Client{
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		logFn: logFn,
	}
}

// ProbeOnce executes a single HTTP GET request to test container response.
func (h *HTTPHealthChecker) ProbeOnce(ctx context.Context, targetURL string, timeout time.Duration) (int, error) {
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, targetURL, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", "zero-downtime-deploy-health-probe/1.0")

	resp, err := h.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode, nil
}
