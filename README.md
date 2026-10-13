# zero-downtime-deploy

> A robust, release engineering command-line deployment tool in Go that orchestrates zero-downtime Blue/Green deployments for Docker containers with automated health checks, atomic traffic switching, and instant rollback on failure.

[![Go Version](https://img.shields.io/badge/Go-1.21%2B-blue.svg)](https://golang.org)
[![License: MIT](https://img.shields.io/badge/License-MIT-green.svg)](LICENSE)
[![Zero Downtime](https://img.shields.io/badge/Deployment-Blue%2FGreen-blueviolet.svg)](#architecture)

---

## Table of Contents

- [Why Zero-Downtime Deployments Matter](#why-zero-downtime-deployments-matter)
- [Architecture & Blue/Green Lifecycle](#architecture--bluegreen-lifecycle)
- [Project Structure](#project-structure)
- [Configuration Schema (`deploy.yaml`)](#configuration-schema-deployyaml)
- [Installation & Build](#installation--build)
- [CLI Command Reference](#cli-command-reference)
- [Automated & Manual Rollback Mechanisms](#automated--manual-rollback-mechanisms)
- [Hands-on Tutorial with Demo App](#hands-on-tutorial-with-demo-app)
- [Running Unit & Integration Tests](#running-unit--integration-tests)
- [License](#license)

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

```
       [ Public Client Ingress (Port 8080) ]
                        │
                        ▼
            ┌───────────────────────┐
            │ Built-in Traffic Router│ (zero dropped requests)
            └───────────┬───────────┘
                        │
            ┌───────────┴───────────┐
            │                       │
     (Active Traffic)       (Health Probing)
            ▼                       ▼
   ┌─────────────────┐     ┌─────────────────┐
   │    SLOT BLUE    │     │   SLOT GREEN    │
   │  (Port 8081)    │     │  (Port 8082)    │
   │ Stable Release  │     │ Candidate v2.0  │
   └─────────────────┘     └─────────────────┘
```

---

## Architecture & Blue/Green Lifecycle

The deployment orchestrator operates as a deterministic 5-phase state machine:

```
[Phase 1: Detection] ────► [Phase 2: Image Pull] ────► [Phase 3: Candidate Start]
                                                                  │
                                                                  ▼
[Phase 5: Traffic Switch] ◄── [Health Pass] ◄─── [Phase 4: Health Probing]
          │                                                       │
          ▼                                                       ▼
[Drain & Decommission]                                     [Health Fail]
                                                                  │
                                                                  ▼
                                                      [🚨 AUTOMATED ROLLBACK]
                                                      (Active stays untouched)
```

1. **Phase 1: Environment Detection**:
   Inspects the local deployment state (`.deploy-state.json`) and Docker container labels to identify the current active slot (`blue` or `green`). Selects the alternate slot and host port as the candidate target.
2. **Phase 2: Container Image Pulling**:
   Pulls the specified image tag from Docker Hub or private registries, falling back to local images if offline.
3. **Phase 3: Candidate Container Spin Up**:
   Cleans up any dangling containers in the target slot, maps host ports (`blue_port` vs `green_port`), injects deployment metadata (`DEPLOY_SLOT`, `APP_NAME`), and starts the candidate container.
4. **Phase 4: Automated Health Validation**:
   Executes proactive polling against the candidate's HTTP health endpoint (`/healthz` or `/`) with configurable initial delay, retries, timeout, and expected HTTP status code.
5. **Phase 5: Atomic Traffic Handoff & Connection Draining**:
   Upon health validation success, atomically redirects live traffic to the candidate slot, updates the deployment state file, waits for the `drain_timeout_seconds` grace period, and gracefully decommissions the old release.

---

## Project Structure

```
zero-downtime-deploy/
├── cmd/
│   └── main.go                 # CLI entry point (Cobra: deploy, rollback, status, router)
├── internal/
│   ├── config.go               # YAML configuration loader & schema validator
│   ├── config_test.go          # Config unit tests
│   ├── docker.go               # Docker Go SDK client manager (lifecycle, ports, labels)
│   ├── health.go               # Resilient HTTP health probe engine with retries
│   ├── health_test.go          # Health checker unit tests
│   ├── orchestrator.go         # Blue/Green state engine with auto-rollback
│   ├── orchestrator_test.go    # Orchestrator & rollback integration tests
│   ├── router.go               # Zero-downtime dynamic HTTP reverse proxy
│   ├── router_test.go          # Router upstream switching unit tests
│   ├── state.go                # Atomic JSON state persistence & audit trail
│   └── state_test.go           # State file unit tests
├── examples/
│   └── demo-app/
│       ├── Dockerfile          # Multi-stage production container build
│       └── main.go             # Sample Go HTTP server with /healthz endpoint
├── deploy.yaml                 # Deployment specification template
├── go.mod                      # Go module definition
├── go.sum                      # Cryptographic checksums of dependencies
├── LICENSE                     # MIT License
├── .gitignore                  # Git ignore rules
└── README.md                   # Comprehensive documentation
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

## CLI Command Reference

### 1. `deploy`
Executes the zero-downtime Blue/Green deployment cycle.

```bash
# Standard deployment using deploy.yaml
./zero-downtime-deploy deploy

# Deploy a specific image tag override
./zero-downtime-deploy deploy --image "myapp:v2.1.0"

# Dry-run validation (simulates deployment without touching Docker)
./zero-downtime-deploy deploy --dry-run

# Specify custom configuration file
./zero-downtime-deploy deploy --config staging.yaml
```

**Output Example:**
```
=================================================================
🚀 STARTING ZERO-DOWNTIME BLUE/GREEN DEPLOYMENT
   Application:  demo-web
   Target Image: nginx:alpine
   Live Port:    8080
=================================================================

[Phase 1/5] Slot & Environment Detection
  • Currently Active Slot: blue (Port: 8081, Image: nginx:1.24)
  • Target Candidate Slot: green (Port: 8082, Image: nginx:alpine)

[Phase 2/5] Pulling Container Image
  • Pulling nginx:alpine from registry...
  ✓ Image pulled successfully

[Phase 3/5] Starting Candidate Container
  • Provisioning container: demo-web-green on host port 8082...
  ✓ Candidate container running (ID: e4a19c72e19d)

[Phase 4/5] Executing Automated Health Validation
  -> Initiating health check on http://127.0.0.1:8082/
  -> Waiting initial delay of 2s for container stabilization...
  [Probe 1/10] http://127.0.0.1:8082/ -> PASS (Status: 200, Response time: 4ms)
  ✓ Candidate container verified healthy in 2.01s (Passed on attempt 1)

[Phase 5/5] Traffic Switching & Decommissioning
  • Atomic traffic handoff: Switching active routing from slot 'blue' -> 'green' (Port: 8082)
  ✓ Deployment state saved successfully to '.deploy-state.json'
  • Draining in-flight connections on old container (demo-web-blue) for 10s...
  • Gracefully stopping old container (demo-web-blue)...
  ✓ Old container decommissioned gracefully

=================================================================
🎉 ZERO-DOWNTIME DEPLOYMENT SUCCEEDED!
   Active Environment: green
   Active Host Port:   8082
   Deployed Image:     nginx:alpine
=================================================================
```

---

### 2. `rollback`
Triggers an immediate rollback to the previous stable release recorded in state.

```bash
./zero-downtime-deploy rollback
```

**Rollback Action:**
- Restores the previous stable image in the standby slot.
- Validates the container's health before cutting traffic over.
- Re-points traffic immediately.
- Safely decommissions the broken release.

---

### 3. `status`
Inspects the current active slot, standby release, live ports, and deployment audit trail.

```bash
./zero-downtime-deploy status
```

**Output Example:**
```
=================================================================
📊 BLUE/GREEN ENVIRONMENT STATUS - demo-web
=================================================================
Active Slot:          green
Active Port:          8082
Active Image:         nginx:alpine
Active Container ID:  e4a19c72e19d
Last Deployed:        Tue, 13 Oct 2026 23:37:00 UTC
-----------------------------------------------------------------
Standby (Stable) Slot: blue
Standby Port:          8081
Standby Image:         nginx:1.24
-----------------------------------------------------------------
Recent Deployment Audit Trail:
  • [23:37:00] Slot: green | Status: success     | Image: nginx:alpine | Promoted to active slot on port 8082
  • [23:25:12] Slot: blue  | Status: success     | Image: nginx:1.24   | Initial release
=================================================================
```

---

### 4. `router`
Launches the built-in dynamic reverse proxy on the live port (e.g. `8080`).

```bash
./zero-downtime-deploy router
# or override port:
./zero-downtime-deploy router --port 8080
```

The router continuously synchronizes with the active slot in `.deploy-state.json` and forwards all incoming client requests directly to the active container slot without dropping in-flight connections.

---

## Automated & Manual Rollback Mechanisms

### 1. Automated Rollback on Health Failure

When a candidate container fails health checks:
```
[Phase 4/5] Executing Automated Health Validation
  -> Initiating health check on http://127.0.0.1:8082/healthz
  [Probe 1/3] http://127.0.0.1:8082/healthz -> UNHEALTHY (Status: 500, expected: 200)
  [Probe 2/3] http://127.0.0.1:8082/healthz -> UNHEALTHY (Status: 500, expected: 200)
  [Probe 3/3] http://127.0.0.1:8082/healthz -> UNHEALTHY (Status: 500, expected: 200)

❌ [HEALTH CHECK FAILED] Candidate container failed validation!

🚨 [AUTOMATED ROLLBACK TRIGGERED]
  • Cleaning up unhealthy candidate container (demo-web-green)...
  ✓ Candidate container stopped and removed
  ✓ Previous active environment 'blue' remains UNTOUCHED and LIVE on port 8081
```

- **Candidate Teardown**: The unverified container is promptly killed and removed so it does not consume host memory or bind ports.
- **Zero Impact on Active**: Traffic routing is never switched. End-users continue hitting the stable active release uninterrupted.
- **Audit Logging**: The failure event is logged to `.deploy-state.json` for post-mortem analysis.

### 2. Manual Emergency Rollback

If a release passes synthetic health checks but encounters application bugs or memory leaks under production traffic, operators can execute:
```bash
./zero-downtime-deploy rollback
```
The CLI immediately flips the active slot back to the previous stable container image and port.

---

## Hands-on Tutorial with Demo App

A test application is included in `examples/demo-app/`.

### Step 1: Build the Sample Container Images
```bash
# Build version 1.0.0
docker build -t myapp:1.0.0 examples/demo-app/

# Build version 2.0.0
docker build -t myapp:2.0.0 examples/demo-app/
```

### Step 2: Start the Traffic Router
In a separate terminal:
```bash
./zero-downtime-deploy router --port 8080
```

### Step 3: Deploy Version 1.0.0 (Slot Blue)
```bash
./zero-downtime-deploy deploy --image "myapp:1.0.0"
```
Test live endpoint:
```bash
curl http://localhost:8080/
# Output: {"status":"ok","slot":"blue","version":"1.0.0","message":"Zero-Downtime Blue/Green Demo"}
```

### Step 4: Deploy Version 2.0.0 (Slot Green)
```bash
./zero-downtime-deploy deploy --image "myapp:2.0.0"
```
Test live endpoint again:
```bash
curl http://localhost:8080/
# Output: {"status":"ok","slot":"green","version":"2.0.0","message":"Zero-Downtime Blue/Green Demo"}
```
Traffic transferred from Blue to Green seamlessly with zero dropped requests!

---