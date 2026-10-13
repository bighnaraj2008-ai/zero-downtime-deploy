package internal

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

func TestTrafficRouter_UpstreamSwitch(t *testing.T) {
	// Setup Blue backend
	blueServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Backend", "blue")
		_, _ = w.Write([]byte("response from blue backend"))
	}))
	defer blueServer.Close()

	// Setup Green backend
	greenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Backend", "green")
		_, _ = w.Write([]byte("response from green backend"))
	}))
	defer greenServer.Close()

	_, bluePortStr, _ := net.SplitHostPort(blueServer.Listener.Addr().String())
	bluePort, _ := strconv.Atoi(bluePortStr)

	_, greenPortStr, _ := net.SplitHostPort(greenServer.Listener.Addr().String())
	greenPort, _ := strconv.Atoi(greenPortStr)

	router := NewTrafficRouter(8080, "", func(string, ...any) {})

	// 1. Initial State: No upstream configured -> 503
	req := httptest.NewRequest(http.MethodGet, "http://localhost:8080/test", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 when no upstream is set, got %d", w.Code)
	}

	// 2. Set upstream to Blue
	if err := router.SetUpstream("blue", bluePort); err != nil {
		t.Fatalf("failed to set upstream to blue: %v", err)
	}

	reqBlue := httptest.NewRequest(http.MethodGet, "http://localhost:8080/api", nil)
	wBlue := httptest.NewRecorder()
	router.ServeHTTP(wBlue, reqBlue)
	if wBlue.Code != http.StatusOK {
		t.Errorf("expected 200 from blue, got %d", wBlue.Code)
	}
	bodyBlue, _ := io.ReadAll(wBlue.Body)
	if string(bodyBlue) != "response from blue backend" {
		t.Errorf("expected blue response body, got '%s'", string(bodyBlue))
	}
	if wBlue.Header().Get("X-Deploy-Slot") != "blue" {
		t.Errorf("expected header X-Deploy-Slot: blue, got '%s'", wBlue.Header().Get("X-Deploy-Slot"))
	}

	// 3. Atomically switch upstream to Green
	if err := router.SetUpstream("green", greenPort); err != nil {
		t.Fatalf("failed to set upstream to green: %v", err)
	}

	reqGreen := httptest.NewRequest(http.MethodGet, "http://localhost:8080/api", nil)
	wGreen := httptest.NewRecorder()
	router.ServeHTTP(wGreen, reqGreen)
	if wGreen.Code != http.StatusOK {
		t.Errorf("expected 200 from green, got %d", wGreen.Code)
	}
	bodyGreen, _ := io.ReadAll(wGreen.Body)
	if string(bodyGreen) != "response from green backend" {
		t.Errorf("expected green response body, got '%s'", string(bodyGreen))
	}
	if wGreen.Header().Get("X-Deploy-Slot") != "green" {
		t.Errorf("expected header X-Deploy-Slot: green, got '%s'", wGreen.Header().Get("X-Deploy-Slot"))
	}
	if wGreen.Header().Get("X-Upstream-Port") != fmt.Sprintf("%d", greenPort) {
		t.Errorf("expected upstream port header %d, got '%s'", greenPort, wGreen.Header().Get("X-Upstream-Port"))
	}
}
