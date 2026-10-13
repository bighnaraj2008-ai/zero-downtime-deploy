package internal

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
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
				// Don't follow redirects automatically so we can inspect status accurately
				return http.ErrUseLastResponse
			},
		},
		logFn: logFn,
	}
}

// Check polls the container's designated endpoint until it passes or retries expire.
func (h *HTTPHealthChecker) Check(ctx context.Context, host string, port int, config HealthConfig) (*HealthCheckResult, error) {
	if host == "" {
		host = "127.0.0.1"
	}
	path := config.Path
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	targetURL := fmt.Sprintf("http://%s:%d%s", host, port, path)
	timeout := time.Duration(config.TimeoutSeconds) * time.Second
	interval := time.Duration(config.IntervalSeconds) * time.Second
	initialDelay := time.Duration(config.InitialDelaySeconds) * time.Second
	maxRetries := config.MaxRetries
	expectedStatus := config.ExpectedStatus
	if expectedStatus == 0 {
		expectedStatus = http.StatusOK
	}

	h.logFn("  -> Initiating health check on %s", targetURL)
	if initialDelay > 0 {
		h.logFn("  -> Waiting initial delay of %v for container stabilization...", initialDelay)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(initialDelay):
		}
	}

	startTime := time.Now()
	var lastErr error
	var lastStatus int

	for attempt := 1; attempt <= maxRetries; attempt++ {
		select {
		case <-ctx.Done():
			return &HealthCheckResult{
				Success:        false,
				Attempts:       attempt,
				TotalDuration:  time.Since(startTime),
				LastStatusCode: lastStatus,
				LastError:      ctx.Err(),
			}, ctx.Err()
		default:
		}

		attemptStart := time.Now()
		reqCtx, cancel := context.WithTimeout(ctx, timeout)
		req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, targetURL, nil)
		if err != nil {
			cancel()
			return nil, fmt.Errorf("failed to construct health check request: %w", err)
		}
		req.Header.Set("User-Agent", "zero-downtime-deploy-health-probe/1.0")

		resp, err := h.client.Do(req)
		elapsed := time.Since(attemptStart)

		if err != nil {
			cancel()
			lastErr = err
			h.logFn("  [Probe %d/%d] %s -> Error: %v (elapsed: %v)", attempt, maxRetries, targetURL, err, elapsed.Round(time.Millisecond))
		} else {
			lastStatus = resp.StatusCode
			// Read body to allow reuse of connection
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			cancel()

			if resp.StatusCode == expectedStatus {
				h.logFn("  [Probe %d/%d] %s -> PASS (Status: %d, Response time: %v)",
					attempt, maxRetries, targetURL, resp.StatusCode, elapsed.Round(time.Millisecond))
				return &HealthCheckResult{
					Success:        true,
					Attempts:       attempt,
					TotalDuration:  time.Since(startTime),
					LastStatusCode: resp.StatusCode,
				}, nil
			}

			lastErr = fmt.Errorf("unexpected status %d, expected %d", resp.StatusCode, expectedStatus)
			h.logFn("  [Probe %d/%d] %s -> UNHEALTHY (Status: %d, expected: %d, elapsed: %v)",
				attempt, maxRetries, targetURL, resp.StatusCode, expectedStatus, elapsed.Round(time.Millisecond))
		}

		if attempt < maxRetries {
			select {
			case <-ctx.Done():
				return &HealthCheckResult{
					Success:        false,
					Attempts:       attempt,
					TotalDuration:  time.Since(startTime),
					LastStatusCode: lastStatus,
					LastError:      ctx.Err(),
				}, ctx.Err()
			case <-time.After(interval):
			}
		}
	}

	return &HealthCheckResult{
		Success:        false,
		Attempts:       maxRetries,
		TotalDuration:  time.Since(startTime),
		LastStatusCode: lastStatus,
		LastError:      fmt.Errorf("health check failed after %d retries: %w", maxRetries, lastErr),
	}, fmt.Errorf("candidate container on %s failed health check after %d retries: %w", targetURL, maxRetries, lastErr)
}
