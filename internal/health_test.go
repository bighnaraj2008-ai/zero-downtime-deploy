package internal

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

func TestHTTPHealthChecker_Success(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok"))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	_, portStr, err := net.SplitHostPort(ts.Listener.Addr().String())
	if err != nil {
		t.Fatalf("failed to parse test server port: %v", err)
	}
	port, _ := strconv.Atoi(portStr)

	checker := NewHTTPHealthChecker(func(format string, args ...any) {})
	cfg := HealthConfig{
		Path:                "/healthz",
		InitialDelaySeconds: 0,
		IntervalSeconds:     1,
		TimeoutSeconds:      2,
		MaxRetries:          3,
		ExpectedStatus:      200,
	}

	result, err := checker.Check(context.Background(), "127.0.0.1", port, cfg)
	if err != nil {
		t.Fatalf("expected health check to pass, got error: %v", err)
	}
	if !result.Success {
		t.Errorf("expected result.Success to be true")
	}
	if result.LastStatusCode != 200 {
		t.Errorf("expected status code 200, got %d", result.LastStatusCode)
	}
}

func TestHTTPHealthChecker_RetryThenPass(t *testing.T) {
	var count int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		val := atomic.AddInt32(&count, 1)
		if val < 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	_, portStr, _ := net.SplitHostPort(ts.Listener.Addr().String())
	port, _ := strconv.Atoi(portStr)

	checker := NewHTTPHealthChecker(func(format string, args ...any) {})
	cfg := HealthConfig{
		Path:                "/",
		InitialDelaySeconds: 0,
		IntervalSeconds:     1,
		TimeoutSeconds:      2,
		MaxRetries:          4,
		ExpectedStatus:      200,
	}

	result, err := checker.Check(context.Background(), "127.0.0.1", port, cfg)
	if err != nil {
		t.Fatalf("expected health check to pass after retry, got: %v", err)
	}
	if !result.Success {
		t.Errorf("expected success")
	}
	if result.Attempts != 2 {
		t.Errorf("expected success on attempt 2, got %d", result.Attempts)
	}
}

func TestHTTPHealthChecker_FailureExceeded(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	_, portStr, _ := net.SplitHostPort(ts.Listener.Addr().String())
	port, _ := strconv.Atoi(portStr)

	checker := NewHTTPHealthChecker(func(format string, args ...any) {})
	cfg := HealthConfig{
		Path:                "/healthz",
		InitialDelaySeconds: 0,
		IntervalSeconds:     1,
		TimeoutSeconds:      1,
		MaxRetries:          2,
		ExpectedStatus:      200,
	}

	result, err := checker.Check(context.Background(), "127.0.0.1", port, cfg)
	if err == nil {
		t.Fatalf("expected error when retries exceeded, got nil")
	}
	if result != nil && result.Success {
		t.Errorf("expected result.Success to be false")
	}
}

func TestHTTPHealthChecker_ContextCancel(t *testing.T) {
	checker := NewHTTPHealthChecker(func(format string, args ...any) {})
	cfg := HealthConfig{
		Path:                "/",
		InitialDelaySeconds: 0,
		IntervalSeconds:     2,
		TimeoutSeconds:      2,
		MaxRetries:          10,
		ExpectedStatus:      200,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	// Probing port 9999 where nothing is running
	_, err := checker.Check(ctx, "127.0.0.1", 9999, cfg)
	if err == nil {
		t.Fatalf("expected context error, got nil")
	}
}
