package internal

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sync"
	"time"
)

// TrafficRouter provides a dynamic reverse proxy listening on the public live port
// that atomically routes traffic to either Blue or Green upstreams.
type TrafficRouter struct {
	mu         sync.RWMutex
	livePort   int
	activeSlot string
	activePort int
	proxy      *httputil.ReverseProxy
	server     *http.Server
	stateFile  string
	targetURL  *url.URL
	logFn      func(format string, args ...any)
}

// NewTrafficRouter creates a TrafficRouter instance.
func NewTrafficRouter(livePort int, stateFile string, logFn func(format string, args ...any)) *TrafficRouter {
	if logFn == nil {
		logFn = func(format string, args ...any) { fmt.Printf(format+"\n", args...) }
	}
	r := &TrafficRouter{
		livePort:  livePort,
		stateFile: stateFile,
		logFn:     logFn,
	}
	return r
}

// SetUpstream atomically updates the target upstream slot and port without dropping connections.
func (r *TrafficRouter) SetUpstream(slot string, port int) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	target, err := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", port))
	if err != nil {
		return fmt.Errorf("invalid upstream url for port %d: %w", port, err)
	}

	r.activeSlot = slot
	r.activePort = port
	r.targetURL = target
	r.proxy = httputil.NewSingleHostReverseProxy(target)

	// Custom error handler for friendly proxy responses during bootup
	r.proxy.ErrorHandler = func(w http.ResponseWriter, req *http.Request, err error) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = fmt.Fprintf(w, `{"error": "bad gateway", "upstream": "%s", "detail": "%v"}`+"\n", target.String(), err)
	}

	r.logFn("  [Traffic Router] Switched upstream -> Slot '%s' (http://127.0.0.1:%d)", slot, port)
	return nil
}

// ServeHTTP handles incoming requests and forwards them to the active upstream.
func (r *TrafficRouter) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.mu.RLock()
	proxy := r.proxy
	slot := r.activeSlot
	port := r.activePort
	r.mu.RUnlock()

	if proxy == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = fmt.Fprintf(w, `{"status": 503, "message": "No active upstream configured in Blue/Green router"}`+"\n")
		return
	}

	// Add tracking headers
	w.Header().Set("X-Deploy-Slot", slot)
	w.Header().Set("X-Upstream-Port", fmt.Sprintf("%d", port))
	proxy.ServeHTTP(w, req)
}

// Start runs the HTTP router server listening on livePort.
func (r *TrafficRouter) Start(addr string) error {
	if addr == "" {
		addr = fmt.Sprintf(":%d", r.livePort)
	}
	r.server = &http.Server{
		Addr:         addr,
		Handler:      r,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}
	r.logFn("Traffic Router listening on %s (Live ingress port)", addr)
	return r.server.ListenAndServe()
}

// Shutdown gracefully stops the proxy server.
func (r *TrafficRouter) Shutdown(ctx context.Context) error {
	if r.server != nil {
		return r.server.Shutdown(ctx)
	}
	return nil
}
