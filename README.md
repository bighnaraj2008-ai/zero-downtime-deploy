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
