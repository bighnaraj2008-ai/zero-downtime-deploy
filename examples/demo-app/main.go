package main

import (
	"fmt"
	"net/http"
	"os"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	slot := os.Getenv("DEPLOY_SLOT")
	if slot == "" {
		slot = "unknown"
	}

	version := os.Getenv("APP_VERSION")
	if version == "" {
		version = "1.0.0"
	}

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"status":"ok","slot":"%s","version":"%s","message":"Zero-Downtime Blue/Green Demo"}`+"\n", slot, version)
	})

	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		// Can simulate failure by setting FAIL_HEALTH=true
		if os.Getenv("FAIL_HEALTH") == "true" {
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprintln(w, `{"health":"failing"}`)
			return
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, `{"health":"ok"}`)
	})

	fmt.Printf("Demo app starting on port %s (Slot: %s, Version: %s)\n", port, slot, version)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		fmt.Fprintf(os.Stderr, "Server error: %v\n", err)
	}
}
