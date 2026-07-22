# api-prober

A cloud-native, platform-independent SRE telemetry stack written in Go. This project implements and visualizes the **4 Golden Signals** (Latency, Traffic, Errors, Saturation) for distributed edge environments.


## Features

* **Dynamic Targets:** Monitored endpoints are dynamically loaded via `targets.csv`. A native background watcher applies updates on the fly, provisioning or gracefully terminating worker goroutines without requiring application restarts.
* **Secure by Default:** Public traffic is forced through HTTPS via Nginx with automated self-signed certificate generation for local development.
* **Multi-Stage & Multi-Arch Build:** Minimal Docker footprint supporting both `amd64` and `arm64` architectures.
* **Fully Encapsulated Stack:** Self-contained environment featuring Go, Prometheus, Alertmanager, Grafana, Nginx, and Httpbin.

## Architecture

All services communicate within an isolated internal Docker bridge network. Public access is routed strictly through the Nginx gateway, forcing an automatic HTTP-to-HTTPS redirect for all endpoints.

```text
api-prober Architecture
│
├── Configuration Layer
│   └── targets.csv (Dynamic target config)
│
├── Core Engine (Go)
│   ├── watchTargets() (Polls targets.csv every 5s)
│   ├── Dynamic Goroutines (Spawns/cancels per target via Context)
│   └── Shared http.Client (Connection pooling & 5s timeouts)
│
├── External Endpoints
│   └── Target APIs (Probed via HTTP GET)
│
└── Observability & Diagnostics Stack
    ├── Structured Logs (JSON stdout via slog)
    ├── Go HTTP Exporter (:8080) ──> Serves /metrics & /debug/pprof
    ├── Prometheus ───────────────> Scrapes /metrics every interval
    └── Grafana ──────────────────> Queries Prometheus via PromQL
```

## 📊 Telemetry & Alerting Preview

### 4 Golden Signals Dashboard
<img src="assets/grafana-dashboard.png" alt="Grafana Dashboard" />

### Emergency Mobile Pushover Alert
<img src="assets/pushover-alert.png" alt="Pushover Alert" width="280" />


### Live Logging Output
<img src="assets/slog-output.png" alt="Live Logging Output" width="800" />

### Container Health
<img src="assets/docker-ps.png" alt="Container Health" />

## Getting Started

### Prerequisites

* Docker and Docker Compose
* GNU Make (optional)
* A Pushover Account (for emergency notifications)

### Secret Configuration

Before starting the stack, create a `.env` file in the root directory to store your Pushover credentials. The stack automatically injects these variables into the Alertmanager configuration at runtime via a custom entrypoint, keeping secrets out of source control:

```env
PUSHOVER_USER_KEY=your_user_key_here
PUSHOVER_API_TOKEN=your_api_token_here
```

### Installation & Lifecycle

You can manage the entire application lifecycle using the provided `Makefile`:

```bash
# View available commands
make

# Build, generate local development TLS certs, and start the entire stack
make up

# Stop all running containers and remove orphans
make down

# Stop containers and completely wipe all persistent telemetry data and certificates
make clean
```

## Configuration

* **Targets:** Define your endpoints in `targets.csv`.
* **Metrics:** Accessible securely from the outside via `https://localhost/metrics` (internally routed to `http://api-prober:8080/metrics`).
* **Dashboard:** Grafana is provisioned automatically with a pre-configured dashboard available at `https://localhost/dashboard/`.

## Alerting & Escalation

Real-time notifications are managed via Prometheus Alertmanager based on thresholds of the 4 Golden Signals.

### Emergency Priority (iOS Silent Mode Bypass)
Alerts are pre-configured with **Priority 2 (Emergency)**. To ensure critical operational alerts break through your phone's silent switch or "Do Not Disturb" focus modes:
1. Open **Settings** on your iOS device.
2. Navigate to **Pushover** -> **Notifications**.
3. Enable **Allow Critical Alerts**.

### Simulating an Alert
To verify the entire alerting pipeline from the edge to your phone, add a failing target to `targets.csv`:

```csv
http://httpbin/status/500
```
## 🗺️ Roadmap & Future Improvements

- [ ] **Bounded Worker Pool:** Refactor the current per-target goroutine model into a fixed worker pool with a buffered job channel to handle thousands of endpoints without risking high resource usage under heavy load.
- [ ] **Dynamic Probe Intervals:** Allow configurable probing intervals per target in `targets.csv`.
- [ ] **Automated Integration Tests:** Add end-to-end tests for target CSV mutations and context cancellation.

## Troubleshooting: Container Networking

Since the entire stack runs fully isolated within a custom Docker bridge network, services resolve each other directly via their service names rather than `localhost`.

### Common Pitfalls:

1. **Targets configuration (`targets.csv`):**
   If your Go application probes services inside the stack (like `httpbin`), ensure the URLs use the container name, not `localhost`:
   ```csv
   http://httpbin/status/200
   ```

2. **Prometheus Targets (`prometheus.yml`):**
   Prometheus pulls the 4 Golden Signals directly from the Go container. The target must point to the service name defined in your compose file:
   ```yaml
   static_configs:
     - targets: ['api-prober:8080']
   ```

3. **Nginx Proxy Routes (`nginx.conf`):**
   If metrics or dashboards are unreachable from the outside, verify that your reverse proxy configuration routes traffic to the correct internal container names (`api-prober` and `grafana`) and that the local TLS certificates are mounted properly via `api_prober_proxy`.