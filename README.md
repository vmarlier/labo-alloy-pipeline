# Alloy Observability Pipeline Lab

This lab environment reproduces a highly available, multi-stage observability pipeline using **Grafana Alloy**, modeled after a production architecture. The goal is to experiment with Alloy as a centralized collector, router, and processor for metrics, logs, and traces.

## Architecture

### 1. Application Layer (Load Generators & App Groups & Edge collector)

We deploy multiple Go-based producers to simulate distinct microservice behaviors:

*   **Group 1 (Errors & Latency):** Receives traffic and intentionally responds slowly or throws 500 errors.
*   **Group 2 (High Span Count):** "Fast Cascade". Receives a request and makes 10+ rapid downstream calls to worker microservices, generating massive traces.
*   **Group 3 (Slow Cascade):** Receives a request and makes multiple sequential slow calls, simulating an N+1 query problem or sluggish downstream dependencies.
*   **Edge Alloy (DaemonSet/Collector):** Scrapes local metrics and forwards OTLP data to the Router.

### 2. Pipeline Layer (Highly Available)

The pipeline components run with **2 replicas each** to validate load balancing and trace affinity:

*   **Alloy Router (2 Replicas):** The central entry point. Uses **DNS Load Balancing** to hash incoming spans by `traceID` and route them to the correct downstream processor. This ensures all spans for a single trace end up on the same tail-sampling node.
*   **Alloy Tail-Sampling (2 Replicas):** Applies sampling policies (e.g., 100% of errors, 1% of successes).
*   **Alloy Span-Metrics (2 Replicas):** Generates RED metrics from traces.
*   **Alloy Grafana (2 Replicas):** Processes and forwards logs.

### 3. Storage & Visualization

* **VictoriaMetrics:** Primary time-series database for metrics.
* **Loki & Tempo:** Local storage for logs and traces.
* **Grafana:** Central dashboarding for both the application data and the **Pipeline Health** (monitoring the collectors themselves).

## How to use

```
# start
make tidy
make up
make open-grafana

# reload
make reload

# stop
make down
make clean
```

## Tooling

- [OrbStack](https://orbstack.dev/)

