# Alloy Observability Pipeline Lab

This lab reproduces a high-availability observability pipeline using **Grafana Alloy**.

## Architecture

### 1. Data Sources (The "Cluster")

* **App Producers (2+ Replicas):** Golang applications generating OTLP metrics, logs, and traces.
* **Edge Alloys (Collector):** * **Metrics:** Directly forwards to **VictoriaMetrics**.
    * **Logs/Traces:** Forwards via gRPC to the load-balanced **Alloy Router** pool.

### 2. Processing Layer (The "Pipeline")

* **Alloy Router (2+ Replicas):** Acts as the central traffic controller. Load balances incoming OTLP data to specialized downstream collectors.
* **Alloy Tail-Sampling:** Applies logic to keep 100% of errors/high-latency traces while sampling healthy traffic.
* **Alloy Span-Metrics:** Computes RED signals (Rate, Errors, Duration) and writes them to VictoriaMetrics.
* **Alloy Grafana (Logs):** Handles log transformation and filtering.

### 3. Storage & Visualization

* **VictoriaMetrics:** Primary time-series database for metrics.
* **Loki & Tempo:** Local storage for logs and traces.
* **Grafana:** Central dashboarding for both the application data and the **Pipeline Health** (monitoring the collectors themselves).
