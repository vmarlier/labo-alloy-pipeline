# Producer Specification: Alloy Observability Pipeline Lab

## 1. Overview
The **Producer** is a configurable, Go-based microservice designed specifically to generate realistic telemetry (traces, metrics, and logs). Its primary purpose is to validate the routing, sampling, and transformation rules within the Grafana Alloy observability pipeline. 

By running multiple instances of this single image with different configurations, the lab can simulate a distributed microservice environment (e.g., Service A calling Service B, long-running batch jobs, high-throughput APIs).

## 2. Core Capabilities

### 2.1. Telemetry Generation (OpenTelemetry)
The application natively integrates with the OpenTelemetry (OTel) Go SDK to emit:
*   **Traces**: Every incoming HTTP request generates a trace span with proper parent-child relationships
*   **Metrics**: HTTP request duration, request counts, and custom business metrics (e.g., `items_processed_total`)
*   **Logs**: Structured JSON logs with automatic `trace_id` and `span_id` extraction from the Go context for trace-to-log correlation

### 2.2. Distributed Context Propagation
When configured to call downstream services, the application injects the W3C Trace Context into outgoing HTTP headers. This ensures that a request originating in "Producer A" and flowing into "Producer B" is stitched together as a single distributed trace.

### 2.3. Fault-Tolerant Exports (Non-Blocking)
The application is resilient to observability backend failures. If the OTLP endpoint (Alloy) is down, unreachable, or slow:
*   The application **does not crash**
*   HTTP request handling **is not blocked or slowed down** by telemetry export failures
*   The OTel exporter retries in the background and gracefully drops telemetry if the buffer fills up or the connection remains down

## 3. Application Behaviors & Endpoints

### 3.1. Traffic Simulation Endpoints

These endpoints simulate different application behaviors to test pipeline rules (sampling, metrics generation, trace processing).

| Endpoint | HTTP Status | Behavior | Use Case |
| :--- | :--- | :--- | :--- |
| `/fast` | `200 OK` | Responds immediately (< 10ms) | High-throughput baseline traffic; tests low-latency trace handling |
| `/slow` | `200 OK` | Sleeps for 1.5 seconds before responding | Tests latency-based sampling policies and slow query detection |
| `/error`| `500 Error` | Sets OTel span status to `codes.Error` | Tests error-based sampling policies and error rate metrics |
| `/random` | Mixed | Randomly returns fast, slow, or error based on configured rates | Simulates realistic mixed traffic patterns |
| `/cascade`| `200 OK` | Ping-pong pattern between services with configurable depth and per-hop delay. Accepts `depth` and `delay` query parameters | Tests distributed tracing with deep call chains; configurable trace duration |
| `/long-job` | `200 OK` | Generates a long-running job with configurable duration. Creates multiple sub-spans (processing chunks) every 10 seconds. Accepts `duration` query parameter (seconds) | Simulates batch jobs, background processing, or long-running operations |
| `/reporting` | `200 OK` | Simulates a realistic reporting job with three phases: data-collection (30%), data-processing (50% with 5 sub-steps), and report-generation (20%). Accepts `duration` query parameter (seconds) | Simulates complex ETL jobs, report generation, or multi-phase processing |

### 3.2. Observability Endpoints (Local Inspection)

These endpoints allow inspection of telemetry data even when the Alloy pipeline is offline or not yet configured:

| Endpoint | Output Format | Description |
| :--- | :--- | :--- |
| `/metrics` | Prometheus Text | Current application and OTel metrics (via OTel Prometheus Exporter) |
| `/logs` | JSON Array | Last 100 generated log lines (in-memory ring buffer) |
| `/traces` | JSON Array | Last 100 generated trace spans (in-memory span exporter) |

## 4. Internal Traffic Generator

To ensure continuous data flow without manual interaction, the application includes a background Goroutine that acts as a traffic generator.

*   **Behavior**: Wakes up at random intervals (100ms - 500ms) and makes HTTP GET requests
*   **Routing**: Hits configured endpoints based on probability weights
*   **Downstream Support**: If downstream URLs are configured, makes requests to external services
*   **Configurable Mix**: Supports fast, slow, error, and long-job traffic patterns

## 5. Configuration (Environment Variables)

The application is entirely configured via environment variables for maximum flexibility.

### Core Configuration

| Variable | Default | Description |
| :--- | :--- | :--- |
| `APP_PORT` | `8080` | HTTP server listening port |
| `OTEL_SERVICE_NAME` | `go-producer` | Service identifier in traces/metrics/logs |
| `OTEL_EXPORTER_OTLP_ENDPOINT`| *(optional)* | Alloy collector URL (e.g., `http://alloy-edge:4317`). If unset, app runs without export |

### Traffic Generation

| Variable | Default | Description |
| :--- | :--- | :--- |
| `TRAFFIC_GEN_ENABLED` | `true` | Enable/disable internal traffic generator |
| `ERROR_RATE` | `10` | Percentage (0-100) of traffic hitting `/error` endpoint |
| `SLOW_RATE` | `10` | Percentage (0-100) of traffic hitting `/slow` endpoint |
| `LONG_JOB_RATE` | `0` | Percentage (0-100) of traffic hitting `/long-job` endpoint |

### Distributed Tracing

| Variable | Default | Description |
| :--- | :--- | :--- |
| `DOWNSTREAM_URLS` | *(empty)* | Comma-separated URLs for downstream calls (e.g., `http://service-b:8080/cascade`) |
| `DOWNSTREAM_CALL_COUNT` | `1` | Initial depth for cascade calls; decrements with each hop |
| `CASCADE_HOP_DELAY_MS` | `0` | Milliseconds to sleep at each cascade hop (controls trace duration) |

### Long-Running Operations

| Variable | Default | Description |
| :--- | :--- | :--- |
| `LONG_JOB_DURATION` | `90` | Default duration (seconds) for `/long-job` endpoint |
| `REPORT_DURATION` | `120` | Default duration (seconds) for `/reporting` endpoint |

## 6. Cascade Ping-Pong Pattern

The cascade implementation creates a realistic ping-pong pattern between services to simulate deep distributed traces.

### Mechanism

1. **Initial Request**: Generator or external caller hits `/cascade` on Service A
2. **Service A**: Receives request with `depth=N` (from `DOWNSTREAM_CALL_COUNT` or query parameter)
3. **Service A → Service B**: Calls Service B's `/cascade?depth=N-1&delay=X`
4. **Service B → Service A**: Calls Service A's `/cascade?depth=N-2&delay=X`
5. **Recursion**: Continues bouncing back and forth until depth reaches 0
6. **Leaf Node**: When `depth=0`, service responds immediately without further calls

### Duration Control

The `CASCADE_HOP_DELAY_MS` environment variable and `delay` query parameter control how long each hop takes:

- **Minimal latency**: `CASCADE_HOP_DELAY_MS=0` → Near-instant hops
- **Medium duration**: `CASCADE_HOP_DELAY_MS=2000` → 2 seconds per hop
- **Long duration**: `CASCADE_HOP_DELAY_MS=8000` → 8 seconds per hop

**Example**: 15 hops × 2000ms = 30-second total trace duration

### Configuration Example

```yaml
service-a:
  environment:
    - DOWNSTREAM_URLS=http://service-b:8080/cascade
    - DOWNSTREAM_CALL_COUNT=15        # Initial depth
    - CASCADE_HOP_DELAY_MS=2000       # 2s per hop

service-b:
  environment:
    - DOWNSTREAM_URLS=http://service-a:8080/cascade
    # No DOWNSTREAM_CALL_COUNT - uses depth from incoming request
```

### Query Parameter Override

Both `depth` and `delay` can be overridden via query parameters:

```bash
# Custom depth of 20 with 1-second hops
curl "http://service-a:8080/cascade?depth=20&delay=1000"
```

## 7. Long-Running Job Endpoints

### 7.1. `/long-job` Endpoint

**Purpose**: Generate traces of configurable duration with realistic sub-span structure.

**Behavior**:
- Default duration: 90 seconds (configurable via `LONG_JOB_DURATION`)
- Creates sub-spans every 10 seconds (processing chunks)
- Logs progress every 3 chunks
- Accepts `duration` query parameter for ad-hoc testing

**Span Structure**:
```
LongJob (root, 90s)
├─ processing-chunk-1 (10s)
├─ processing-chunk-2 (10s)
├─ processing-chunk-3 (10s)
├─ ... (continues)
└─ processing-chunk-9 (10s)
```

**Span Attributes**:
- `handler.type`: "long-job"
- `job.duration_seconds`: Configured or requested duration
- `chunk.number`: Sequential chunk identifier (1-N)

**Examples**:
```bash
# Use default duration (90s)
curl http://service:8080/long-job

# Custom duration (120s)
curl http://service:8080/long-job?duration=120

# Very long trace (300s)
curl http://service:8080/long-job?duration=300
```

### 7.2. `/reporting` Endpoint

**Purpose**: Simulate realistic reporting/batch job with multiple phases and sub-operations.

**Behavior**:
- Default duration: 120 seconds (configurable via `REPORT_DURATION`)
- Three sequential phases with realistic sub-structure
- Logs after each phase completion
- Accepts `duration` query parameter

**Phase Breakdown**:
1. **Data Collection** (30% of time): Single span representing data fetching
2. **Data Processing** (50% of time): Parent span with 5 sequential processing steps
3. **Report Generation** (20% of time): Single span for output creation

**Span Structure**:
```
ReportingJob (root, 120s)
├─ data-collection (36s)
├─ data-processing (60s)
│  ├─ processing-step-1 (12s)
│  ├─ processing-step-2 (12s)
│  ├─ processing-step-3 (12s)
│  ├─ processing-step-4 (12s)
│  └─ processing-step-5 (12s)
└─ report-generation (24s)
```

**Span Attributes**:
- `handler.type`: "reporting"
- `report.type`: "financial-summary"
- `report.duration_seconds`: Total duration
- `records.count`: 1000000 (on data-collection span)
- `processing.type`: "aggregation" (on data-processing span)
- `format`: "pdf" (on report-generation span)
- `step.number`: 1-5 (on processing-step spans)

**Examples**:
```bash
# Use default duration (120s)
curl http://service:8080/reporting

# Faster report (90s)
curl http://service:8080/reporting?duration=90

# Extended report (180s)
curl http://service:8080/reporting?duration=180
```

## 8. Logging Standard

The application uses Go's `slog` package configured with a JSON handler.

### Standard Log Format

```json
{
  "time": "2026-05-11T10:15:30.123Z",
  "level": "INFO",
  "msg": "Handled fast request successfully",
  "trace_id": "5b8aa5a2d2c872e8321cf37308d69df2",
  "span_id": "515f9a4cb96e46f1",
  "handler.type": "fast"
}
```

### Long Job Progress Logs

```json
{"level":"INFO","msg":"Starting long job","duration_seconds":90}
{"level":"INFO","msg":"Long job progress","chunk":3,"total_chunks":9}
{"level":"INFO","msg":"Long job progress","chunk":6,"total_chunks":9}
{"level":"INFO","msg":"Long job completed","duration_seconds":90}
```

### Reporting Job Phase Logs

```json
{"level":"INFO","msg":"Starting report generation","duration_seconds":120}
{"level":"INFO","msg":"Data collection complete","phase":"collection"}
{"level":"INFO","msg":"Data processing complete","phase":"processing"}
{"level":"INFO","msg":"Report generation complete","phase":"generation"}
{"level":"INFO","msg":"Report completed successfully","total_duration_seconds":120}
```

### Cascade Logs

```json
{"level":"INFO","msg":"Cascade request received","remaining_depth":15,"hop_delay_ms":2000}
{"level":"INFO","msg":"Making cascade call","url":"http://worker:8080/cascade?depth=14&delay=2000","depth":14}
{"level":"INFO","msg":"Cascade completed - leaf node reached"}
```

## 9. Span Attributes Reference

### Common Attributes (All Endpoints)

- `handler.type`: Endpoint identifier (fast, slow, error, cascade, long-job, reporting, random)
- `service.name`: From `OTEL_SERVICE_NAME` environment variable
- `http.method`: HTTP method (GET, POST, etc.)
- `http.status_code`: HTTP response status code

### Cascade-Specific

- `cascade.remaining_depth`: Current depth in cascade chain
- `cascade.hop_delay_ms`: Configured delay per hop in milliseconds
- `cascade.leaf`: Boolean, true only on leaf nodes (depth=0)

### Long Job Specific

- `job.duration_seconds`: Total configured duration
- `chunk.number`: Sub-span identifier (1, 2, 3, ... N)

### Reporting Job Specific

- `report.type`: Report type identifier ("financial-summary")
- `report.duration_seconds`: Total configured duration
- `records.count`: Number of records processed (1000000)
- `processing.type`: Type of processing ("aggregation")
- `format`: Output format ("pdf")
- `step.number`: Processing step number (1-5)

## 10. Performance Characteristics

### Resource Usage per Instance

- **Memory**: 50-100MB baseline
- **CPU**: 5-15% under typical load
- **Network**: 1-10 KB/s (depends on OTLP traffic volume)

### Trace Generation Rates (Typical)

| Endpoint Type | Rate | Span Count | Avg Duration |
|--------------|------|-----------|-------------|
| Fast | ~10/sec | 1 | <10ms |
| Slow | ~2/sec | 1 | 1.5s |
| Cascade (depth=15, delay=2s) | ~0.5/sec | 15 | 30s |
| Long Job (90s) | ~0.2/sec | 9 | 90s |
| Reporting (120s) | ~0.15/sec | 9 | 120s |

### Traffic Generator Behavior

When `TRAFFIC_GEN_ENABLED=true`:
- Wakes every 100-500ms (randomized)
- Selects endpoint based on configured rates
- If `DOWNSTREAM_URLS` is set, prefers downstream calls
- Otherwise, hits local endpoints based on `ERROR_RATE`, `SLOW_RATE`, `LONG_JOB_RATE`

## 11. Use Cases

### Scenario 1: High-Throughput API Testing
```yaml
bff-service:
  environment:
    - OTEL_SERVICE_NAME=bff-api
    - TRAFFIC_GEN_ENABLED=true
    - ERROR_RATE=5
    - SLOW_RATE=10
```
**Result**: Continuous fast traffic with occasional errors and slow requests

### Scenario 2: Distributed Microservice Chain
```yaml
service-a:
  environment:
    - DOWNSTREAM_URLS=http://service-b:8080/cascade
    - DOWNSTREAM_CALL_COUNT=10
    - CASCADE_HOP_DELAY_MS=500

service-b:
  environment:
    - DOWNSTREAM_URLS=http://service-a:8080/cascade
```
**Result**: 10-hop ping-pong traces, 5 seconds total duration

### Scenario 3: Batch Job Simulation
```yaml
batch-processor:
  environment:
    - OTEL_SERVICE_NAME=batch-job
    - TRAFFIC_GEN_ENABLED=true
    - LONG_JOB_RATE=100  # All traffic → long jobs
    - LONG_JOB_DURATION=180
```
**Result**: Continuous 3-minute job traces with chunked sub-spans

### Scenario 4: Complex ETL Pipeline
```yaml
reporting-service:
  environment:
    - OTEL_SERVICE_NAME=reporting-engine
    - TRAFFIC_GEN_ENABLED=true
    - LONG_JOB_RATE=100
    - REPORT_DURATION=120
```
**Result**: 2-minute multi-phase reporting job traces with realistic structure

### Scenario 5: Mixed Workload
```yaml
mixed-service:
  environment:
    - TRAFFIC_GEN_ENABLED=true
    - ERROR_RATE=5
    - SLOW_RATE=15
    - LONG_JOB_RATE=10
    - DOWNSTREAM_URLS=http://other-service:8080/fast
```
**Result**: 10% long jobs, 15% slow requests, 5% errors, 70% fast traffic to downstream
