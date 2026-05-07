# Producer Specification: Alloy Observability Pipeline Lab

## 1. Overview
The **Producer** is a configurable, Go-based microservice designed specifically to generate realistic telemetry (traces, metrics, and logs). Its primary purpose is to validate the routing, sampling, and transformation rules within the Grafana Alloy observability pipeline. 

By running multiple instances of this single image with different configurations, the lab can simulate a distributed microservice environment (e.g., Service A calling Service B).

## 2. Core Capabilities

### 2.1. Telemetry Generation (OpenTelemetry)
The application must natively integrate with the OpenTelemetry (OTel) Go SDK to emit:
*   **Traces**: Every incoming HTTP request must generate a trace span. 
* [118;1:3u  **Metrics**: HTTP request duration, request counts, and custom business metrics (e.g., `items_processed_total`).
*   **Logs**: Structured JSON logs. Logs must automatically extract the `trace_id` and `span_id` from the Go context and append them to the JSON payload for trace-to-log correlation.

### 2.2. Distributed Context Propagation
When configured to call downstream services, the application must inject the W3C Trace Context into the outgoing HTTP headers. This ensures that a request originating in "Producer A" and flowing into "Producer B" is stitched together as a single distributed trace.

### 2.3. Fault-Tolerant Exports (Non-Blocking)
The application must be resilient to observability backend failures. If the OTLP endpoint (Alloy) is down, unreachable, or slow:
*   The application **must not crash**.
*   HTTP request handling **must not be blocked or slowed down** by telemetry export failures.
*   The OTel exporter should be configured to retry in the background and gracefully drop telemetry if the buffer fills up or the connection remains down.

## 3. Application Behaviors & Endpoints

### 3.1. Traffic Generation Endpoints (Pipeline Testing)
These endpoints simulate different application behaviors to test pipeline rules (sampling, metrics generation).

| Endpoint | HTTP Status | Behavior | Pipeline Test Goal |
| :--- | :--- | :--- | :--- |
| `/fast` | `200 OK` | Responds immediately. | Tests if tail-sampling correctly drops a high percentage of healthy traffic. Validates RED metrics. |
| `/slow` | `200 OK` | Sleeps for 1.5 seconds before responding. | Proves tail-sampling correctly identifies and keeps high-latency traces. |
| `/error`| `500 Error` | Sets OTel span status to `codes.Error`. | Proves tail-sampling bypasses rate limits to retain **100%** of traces containing errors. |
| `/random` | Mixed | Rolls a random number against `ERROR_RATE` and `SLOW_RATE` to determine its own behavior dynamically. |
| `/cascade`| `200 OK` | Triggers a loop, making `DOWNSTREAM_CALL_COUNT` requests to `DOWNSTREAM_URLS`, passing the trace context to generate massive traces. |

### 3.2. Local Observability Endpoints (Standalone Viewing)
To allow inspection of telemetry data even when the Alloy pipeline is offline or not yet configured, the application must expose local endpoints:

| Endpoint | Output Format | Description | Implementation Detail |
| :--- | :--- | :--- | :--- |
| `/metrics` | Prometheus Text | Exposes current application and OTel metrics directly. | Powered by the OTel Prometheus Exporter. |
| `/logs` | JSON Array | Displays the last *N* (e.g., 100) generated log lines. | Implemented via a custom in-memory ring buffer hooked into the `slog` handler. |
| `/traces` | JSON Array | Displays the last *N* (e.g., 100) generated trace spans. | Implemented via a custom in-memory OTel `SpanExporter` running alongside the OTLP exporter. |

## 4. Internal Traffic Generator

To ensure continuous data flow on the Grafana dashboards without manual interaction, the app includes a background Goroutine that acts as a traffic generator.

*   **Behavior**: Wakes up at random intervals (e.g., 100ms - 500ms) and makes HTTP GET requests.
*   **Routing**: Hits its own endpoints (`/fast`, `/slow`, `/error`) based on configurable weights/probabilities.
*   **Downstream Calls**: If downstream URLs are configured, it will make requests to those external services instead of, or in addition to, its own endpoints.

## 5. Configuration (Environment Variables)

The application is highly reusable and entirely configured via environment variables.

| Variable | Default | Description |
| :--- | :--- | :--- |
| `APP_PORT` | `8080` | The port the HTTP server listens on. |
| `OTEL_SERVICE_NAME` | `go-producer` | Identifier for the service (e.g., `producer-frontend`). |
| `OTEL_EXPORTER_OTLP_ENDPOINT`| *(optional)* | URL of the Edge Alloy collector (e.g., `http://alloy-edge:4317`). If empty/unreachable, app continues running. |
| `TRAFFIC_GEN_ENABLED` | `true` | If `true`, the app generates continuous background traffic. |
| `ERROR_RATE` | `10` | Percentage of generated traffic that should hit the `/error` endpoint (0-100). |
| `SLOW_RATE` | `10` | Percentage of generated traffic that should hit the `/slow` endpoint (0-100). |
| `DOWNSTREAM_URLS` | *(empty)* | Comma-separated list of URLs to call to test distributed tracing. |
| `DOWNSTREAM_CALL_COUNT`| `1` | Number of times to loop the downstream call when the `/cascade` endpoint is hit. |

## 6. Logging Standard

The application must use Go's `slog` package configured with a JSON handler. 

**Expected Log Output Format:**
```json
{
  "time": "2026-05-07T10:15:30.123Z",
  "level": "INFO",
  "msg": "Handled fast request successfully",
  "trace_id": "5b8aa5a2d2c872e8321cf37308d69df2",
  "span_id": "515f9a4cb96e46f1",
  "handler.type": "fast"
}
```

## 7. Packaging & Deployment

The application will be packaged using a multi-stage Docker build to ensure a minimal footprint.
*   **Build Stage**: `golang:1.26.2-alpine` (compiles the binary with `CGO_ENABLED=0`).
*   **Runtime Stage**: `gcr.io/distroless/static-debian12` (secure, minimal execution environment).
