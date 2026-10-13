# zero-downtime-deploy

> A robust, release engineering command-line deployment tool in Go that orchestrates zero-downtime Blue/Green deployments for Docker containers with automated health checks, atomic traffic switching, and instant rollback on failure.

[![Go Version](https://img.shields.io/badge/Go-1.21%2B-blue.svg)](https://golang.org)
[![License: MIT](https://img.shields.io/badge/License-MIT-green.svg)](LICENSE)
[![Zero Downtime](https://img.shields.io/badge/Deployment-Blue%2FGreen-blueviolet.svg)](#architecture)

---

## Why Zero-Downtime Deployments Matter

In modern release engineering, high-availability service level agreements (SLAs) like 99.99% ("four nines") allow for less than **52 minutes of total downtime per year** across all planned maintenance and unexpected outages.

Traditional in-place container redeployments (e.g. `docker stop && docker rm && docker run`) introduce unavoidable downtime windows ranging from several seconds to minutes while the old container shuts down, new layers pull, and the application initializes. During this black-hole window:
- In-flight HTTP transactions and payment sessions are dropped.
- Upstream load balancers and reverse proxies return HTTP 502/503 errors.
- Database connections are abruptly severed without graceful termination.
- User experience and business revenue suffer.

### The Blue/Green Solution

**Zero-Downtime Blue/Green Deployment** eliminates this vulnerability:
1. **Two Identical Environments**: A stable live slot (`blue`) and an idle alternate slot (`green`).
2. **Pre-Flight Health Verification**: The candidate version is spun up in isolation on its dedicated port. No user traffic is routed to it until exhaustive HTTP health probes pass.
3. **Atomic Traffic Switching**: Live ingress traffic is instantly cut over to the healthy candidate.
4. **Connection Draining (Grace Period)**: The older container remains active for a configurable drain window to fulfill in-flight client connections before graceful termination.
5. **Instant Automated Rollback**: If the candidate fails health checks or crashes on startup, the tool immediately aborts, stops the failed container, preserves the current active release untouched, and logs the incident.

---

## Installation & Build

### Prerequisites
- [Go 1.21+](https://golang.org/dl/)
- [Docker Engine](https://docs.docker.com/engine/) or [Docker Desktop](https://www.docker.com/products/docker-desktop/)

### Compiling Locally

```powershell
# Inside the zero-downtime-deploy folder:
go build -o zero-downtime-deploy.exe ./cmd/main.go
```

On Linux or macOS:
```bash
go build -o zero-downtime-deploy ./cmd/main.go
chmod +x zero-downtime-deploy
```

Verify installation:
```bash
./zero-downtime-deploy version
# Output: zero-downtime-deploy v1.0.0 (Go Release Engineering CLI)
```

---

## Configuration Schema (`deploy.yaml`)

Below is the complete configuration format with all supported fields:

```yaml
# Application identifier
app_name: "demo-web"

# Container image and tag
image: "nginx"
tag: "alpine"

# Target port inside the container
container_port: 80

# Isolated host ports allocated to Blue and Green instances
blue_port: 8081
green_port: 8082

# Public live ingress port serviced by the zero-downtime router
live_port: 8080

# Health check parameters for candidate verification
health_check:
  path: "/"                   # Endpoint polled before traffic handoff
  initial_delay_seconds: 2    # Wait duration before first probe
  interval_seconds: 2         # Duration between consecutive probes
  timeout_seconds: 5          # HTTP request timeout per probe
  max_retries: 10             # Number of attempts before failure
  expected_status: 200        # HTTP status code considered healthy

# Connection draining & safety policies
drain_timeout_seconds: 10     # Grace period before stopping old container
rollback_on_failure: true     # Automatically revert on health check failure

# Environment variables injected into containers
env:
  ENV: "production"
  RELEASE_STAGE: "stable"

# State persistence file
state_file: ".deploy-state.json"
```

### Configuration Fields Reference

| Parameter | Type | Default | Description |
|---|---|---|---|
| `app_name` | `string` | *(Required)* | Unique name for the application and container naming prefix. |
| `image` | `string` | *(Required)* | Docker image repository (e.g. `nginx`, `myapp`). |
| `tag` | `string` | `latest` | Image tag to deploy (e.g. `v1.2.0`, `alpine`). |
| `container_port` | `int` | `80` | Port exposed by the application inside the container. |
| `blue_port` | `int` | `8081` | Host port mapped to the Blue container. |
| `green_port` | `int` | `8082` | Host port mapped to the Green container. |
| `live_port` | `int` | `8080` | Live ingress port handled by the reverse proxy. |
| `health_check.path` | `string` | `"/"` | Path probed by the HTTP health checker (e.g. `/healthz`). |
| `health_check.initial_delay_seconds` | `int` | `2` | Seconds to wait before the first health probe. |
| `health_check.interval_seconds` | `int` | `2` | Interval between polling attempts. |
| `health_check.timeout_seconds` | `int` | `5` | Timeout for each individual HTTP probe. |
| `health_check.max_retries` | `int` | `10` | Maximum number of probe attempts before failing. |
| `health_check.expected_status` | `int` | `200` | Expected HTTP status code. |
| `drain_timeout_seconds` | `int` | `5` | Time to wait after traffic switch before stopping old release. |
| `rollback_on_failure` | `bool` | `true` | If `true`, aborts and removes candidate on probe failure. |
| `state_file` | `string` | `".deploy-state.json"` | Path to JSON state file. |
