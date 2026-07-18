# QuadraSign

A cloud-native, platform-independent SRE telemetry stack written in Go. This project implements and visualizes the **4 Golden Signals** (Latency, Traffic, Errors, Saturation) for distributed edge environments.

## Features

* **Dynamic Targets:** Monitored endpoints are dynamically loaded via `targets.csv`.
* **Multi-Stage & Multi-Arch Build:** Minimal Docker footprint supporting both `amd64` and `arm64` architectures.
* **Fully Encapsulated Stack:** Self-contained environment featuring Go, Prometheus, Grafana, Nginx, and Httpbin.

## Architecture

All services communicate securely within an isolated internal Docker bridge network. Public access is routed strictly through the Nginx gateway.

## Getting Started

### Prerequisites

* Docker and Docker Compose
* GNU Make (optional)

### Installation & Lifecycle

You can manage the entire application lifecycle using the provided `Makefile`:

```bash
# View available commands
make

# Build and start the entire stack
make up

# Stop all running containers
make down

# Stop containers and completely wipe all persistent telemetry data
make clean
```

## Configuration

* **Targets:** Define your endpoints in `targets.csv`.
* **Metrics:** Accessible internally via `http://quadrasign:8080/metrics`.
* **Dashboard:** Grafana is provisioned automatically with a pre-configured dashboard.

## Troubleshooting: Container Networking

Since the entire stack runs fully isolated within a custom Docker bridge network, services resolve each other directly via their service names rather than `localhost` or host-specific gateways.

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
     - targets: ['quadrasign:8080']
   ```

3. **Nginx Proxy Routes (`nginx.conf`):**
   If metrics or dashboards are unreachable from the outside, verify that your reverse proxy configuration routes traffic to the correct internal container names (`quadrasign` and `grafana`).

## Roadmap

* **Alerting Integration:** Add Prometheus Alertmanager to configure real-time notifications based on the 4 Golden Signals thresholds.