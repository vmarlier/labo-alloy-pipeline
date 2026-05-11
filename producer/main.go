package main

import (
	"context"
	"encoding/json"
	"log"
	"log/slog"
	"math/rand"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
	otrace "go.opentelemetry.io/otel/trace"
)

// --- In-Memory Storage for /logs and /traces ---

type MemoryStore struct {
	mu     sync.RWMutex
	logs   []map[string]interface{}
	traces []interface{}
	limit  int
}

func (s *MemoryStore) AddLog(entry map[string]interface{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logs = append(s.logs, entry)
	if len(s.logs) > s.limit {
		s.logs = s.logs[1:]
	}
}

func (s *MemoryStore) AddTrace(span interface{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.traces = append(s.traces, span)
	if len(s.traces) > s.limit {
		s.traces = s.traces[1:]
	}
}

var store = &MemoryStore{limit: 100}

// --- Custom Slog Handler for OTel Correlation ---

type OTelHandler struct {
	slog.Handler
}

func (h *OTelHandler) Handle(ctx context.Context, r slog.Record) error {
	spanContext := otrace.SpanContextFromContext(ctx)
	if spanContext.HasTraceID() {
		r.AddAttrs(
			slog.String("trace_id", spanContext.TraceID().String()),
			slog.String("span_id", spanContext.SpanID().String()),
		)
	}

	// Capture for /logs endpoint
	h.Handler.Handle(ctx, r)
	logMap := make(map[string]interface{})
	logMap["time"] = r.Time.Format(time.RFC3339)
	logMap["level"] = r.Level.String()
	logMap["msg"] = r.Message
	if spanContext.HasTraceID() {
		logMap["trace_id"] = spanContext.TraceID().String()
		logMap["span_id"] = spanContext.SpanID().String()
	}
	store.AddLog(logMap)

	return nil
}

// --- OTel Trace Exporter for /traces ---

type MemTraceExporter struct{}

func (e *MemTraceExporter) ExportSpans(ctx context.Context, spans []trace.ReadOnlySpan) error {
	for _, s := range spans {
		data := map[string]interface{}{
			"name":     s.Name(),
			"trace_id": s.SpanContext().TraceID().String(),
			"span_id":  s.SpanContext().SpanID().String(),
			"status":   s.Status().Code.String(),
		}
		store.AddTrace(data)
	}
	return nil
}
func (e *MemTraceExporter) Shutdown(ctx context.Context) error { return nil }

// --- Main Logic ---

func main() {
	serviceName := getEnv("OTEL_SERVICE_NAME", "go-producer")
	ctx := context.Background()

	// 1. Setup Resource
	res, _ := resource.Merge(resource.Default(), resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceNameKey.String(serviceName),
	))

	// 2. Setup Metrics (Prometheus)
	promExporter, _ := prometheus.New()
	meterProvider := metric.NewMeterProvider(metric.WithReader(promExporter), metric.WithResource(res))
	otel.SetMeterProvider(meterProvider)

	// 3. Setup Tracing (OTLP + Memory)
	var tracerOpts []trace.TracerProviderOption
	tracerOpts = append(tracerOpts, trace.WithResource(res))
	tracerOpts = append(tracerOpts, trace.WithBatcher(&MemTraceExporter{}))

	otlpEndpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	if otlpEndpoint != "" {
		endpoint := strings.TrimPrefix(otlpEndpoint, "http://")
		otlpExp, err := otlptracegrpc.New(ctx,
			otlptracegrpc.WithInsecure(),
			otlptracegrpc.WithEndpoint(endpoint),
		)
		if err == nil {
			tracerOpts = append(tracerOpts, trace.WithBatcher(otlpExp))
		}
	}
	tp := trace.NewTracerProvider(tracerOpts...)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})

	// 4. Logger Setup
	logger := slog.New(&OTelHandler{Handler: slog.NewJSONHandler(os.Stdout, nil)})
	slog.SetDefault(logger)

	// 5. HTTP Routes
	mux := http.NewServeMux()

	// Pipeline Testing Endpoints
	mux.Handle("/fast", otelhttp.NewHandler(http.HandlerFunc(fastHandler), "fast"))
	mux.Handle("/slow", otelhttp.NewHandler(http.HandlerFunc(slowHandler), "slow"))
	mux.Handle("/error", otelhttp.NewHandler(http.HandlerFunc(errorHandler), "error"))
	mux.Handle("/random", otelhttp.NewHandler(http.HandlerFunc(handleRandom), "RandomRequest"))
	mux.Handle("/cascade", otelhttp.NewHandler(http.HandlerFunc(handleCascade), "CascadeRequest"))

	// NEW: Long-running trace endpoints for tail-sampling testing
	mux.Handle("/long-job", otelhttp.NewHandler(http.HandlerFunc(handleLongJob), "LongJob"))
	mux.Handle("/reporting", otelhttp.NewHandler(http.HandlerFunc(handleReporting), "ReportingJob"))

	// Observability Endpoints
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/logs", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(store.logs)
	})
	mux.HandleFunc("/traces", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(store.traces)
	})

	// 6. Traffic Generator
	if getEnv("TRAFFIC_GEN_ENABLED", "true") == "true" {
		go startTrafficGenerator()
	}

	port := getEnv("APP_PORT", "8080")
	slog.Info("Starting server", "port", port, "service", serviceName)
	log.Fatal(http.ListenAndServe(":"+port, mux))
}

// --- Handlers ---

func fastHandler(w http.ResponseWriter, r *http.Request) {
	span := otrace.SpanFromContext(r.Context())
	span.SetAttributes(attribute.String("handler.type", "fast"))
	slog.InfoContext(r.Context(), "Handled fast request successfully", "handler.type", "fast")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}

func slowHandler(w http.ResponseWriter, r *http.Request) {
	span := otrace.SpanFromContext(r.Context())
	span.SetAttributes(attribute.String("handler.type", "slow"))
	time.Sleep(1500 * time.Millisecond)
	slog.InfoContext(r.Context(), "Handled slow request", "handler.type", "slow")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Slow OK"))
}

func errorHandler(w http.ResponseWriter, r *http.Request) {
	span := otrace.SpanFromContext(r.Context())
	span.SetAttributes(attribute.String("handler.type", "error"))
	span.SetStatus(codes.Error, "simulated error")
	slog.ErrorContext(r.Context(), "Handled error request", "handler.type", "error")
	http.Error(w, "Internal Server Error", http.StatusInternalServerError)
}

func handleRandom(w http.ResponseWriter, r *http.Request) {
	errorRate := getEnvAsInt("ERROR_RATE", 0)
	slowRate := getEnvAsInt("SLOW_RATE", 0)

	roll := rand.Intn(100)
	if roll < errorRate {
		errorHandler(w, r)
	} else if roll < (errorRate + slowRate) {
		slowHandler(w, r)
	} else {
		fastHandler(w, r)
	}
}

// NEW: Long-running job handler to simulate traces that exceed decision_wait
func handleLongJob(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	span := otrace.SpanFromContext(ctx)
	span.SetAttributes(attribute.String("handler.type", "long-job"))

	// Get duration from query param or env var (in seconds)
	duration := getEnvAsInt("LONG_JOB_DURATION", 90)
	if durationParam := r.URL.Query().Get("duration"); durationParam != "" {
		if d, err := strconv.Atoi(durationParam); err == nil && d > 0 {
			duration = d
		}
	}

	span.SetAttributes(attribute.Int("job.duration_seconds", duration))
	slog.InfoContext(ctx, "Starting long job", "duration_seconds", duration)

	tracer := otel.Tracer("long-job")
	// Simulate work with multiple sub-spans to create realistic trace structure
	chunks := duration / 10
	if chunks < 1 {
		chunks = 1
	}
	for i := 0; i < chunks; i++ {
		_, subSpan := tracer.Start(ctx, "processing-chunk")
		subSpan.SetAttributes(attribute.Int("chunk.number", i+1))
		time.Sleep(10 * time.Second)
		subSpan.End()

		if i%3 == 0 {
			slog.InfoContext(ctx, "Long job progress", "chunk", i+1, "total_chunks", chunks)
		}
	}

	slog.InfoContext(ctx, "Long job completed", "duration_seconds", duration)
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Long Job Completed"))
}

// NEW: Reporting job handler simulating complex data processing
func handleReporting(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	span := otrace.SpanFromContext(ctx)
	span.SetAttributes(
		attribute.String("handler.type", "reporting"),
		attribute.String("report.type", "financial-summary"),
	)

	// Configurable duration for the entire report
	reportDuration := getEnvAsInt("REPORT_DURATION", 120)
	if durationParam := r.URL.Query().Get("duration"); durationParam != "" {
		if d, err := strconv.Atoi(durationParam); err == nil && d > 0 {
			reportDuration = d
		}
	}

	span.SetAttributes(attribute.Int("report.duration_seconds", reportDuration))
	slog.InfoContext(ctx, "Starting report generation", "duration_seconds", reportDuration)

	tracer := otel.Tracer("reporting")

	// Phase 1: Data Collection (30% of time)
	collectionCtx, collectionSpan := tracer.Start(ctx, "data-collection")
	collectionSpan.SetAttributes(attribute.Int("records.count", 1000000))
	time.Sleep(time.Duration(float64(reportDuration)*0.3) * time.Second)
	slog.InfoContext(collectionCtx, "Data collection complete", "phase", "collection")
	collectionSpan.End()

	// Phase 2: Data Processing (50% of time)
	processingCtx, processingSpan := tracer.Start(ctx, "data-processing")
	processingSpan.SetAttributes(attribute.String("processing.type", "aggregation"))

	// Simulate multiple processing steps
	steps := 5
	stepDuration := time.Duration(float64(reportDuration)*0.5/float64(steps)) * time.Second
	for i := 0; i < steps; i++ {
		_, stepSpan := tracer.Start(processingCtx, "processing-step")
		stepSpan.SetAttributes(attribute.Int("step.number", i+1))
		time.Sleep(stepDuration)
		stepSpan.End()
	}

	slog.InfoContext(processingCtx, "Data processing complete", "phase", "processing")
	processingSpan.End()

	// Phase 3: Report Generation (20% of time)
	generationCtx, generationSpan := tracer.Start(ctx, "report-generation")
	generationSpan.SetAttributes(attribute.String("format", "pdf"))
	time.Sleep(time.Duration(float64(reportDuration)*0.2) * time.Second)
	slog.InfoContext(generationCtx, "Report generation complete", "phase", "generation")
	generationSpan.End()

	slog.InfoContext(ctx, "Report completed successfully", "total_duration_seconds", reportDuration)
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Report Generated"))
}

func handleCascade(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	span := otrace.SpanFromContext(ctx)
	span.SetAttributes(attribute.String("handler.type", "cascade"))

	// Get remaining depth from query parameter or header
	remainingDepth := getEnvAsInt("DOWNSTREAM_CALL_COUNT", 1)
	if depthParam := r.URL.Query().Get("depth"); depthParam != "" {
		if depth, err := strconv.Atoi(depthParam); err == nil && depth >= 0 {
			remainingDepth = depth
		}
	}

	// Get per-hop delay configuration (in milliseconds)
	hopDelay := getEnvAsInt("CASCADE_HOP_DELAY_MS", 0)
	if delayParam := r.URL.Query().Get("delay"); delayParam != "" {
		if delay, err := strconv.Atoi(delayParam); err == nil && delay >= 0 {
			hopDelay = delay
		}
	}

	span.SetAttributes(
		attribute.Int("cascade.remaining_depth", remainingDepth),
		attribute.Int("cascade.hop_delay_ms", hopDelay),
	)
	slog.InfoContext(ctx, "Cascade request received", "remaining_depth", remainingDepth, "hop_delay_ms", hopDelay)

	// Apply per-hop delay to simulate realistic processing time
	if hopDelay > 0 {
		time.Sleep(time.Duration(hopDelay) * time.Millisecond)
	}

	// If depth is 0, we've reached the end of the cascade
	if remainingDepth <= 0 {
		span.SetAttributes(attribute.Bool("cascade.leaf", true))
		slog.InfoContext(ctx, "Cascade completed - leaf node reached")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("Cascade Leaf"))
		return
	}

	downstreamURLs := strings.Split(os.Getenv("DOWNSTREAM_URLS"), ",")
	if len(downstreamURLs) > 0 && downstreamURLs[0] != "" {
		client := http.Client{Transport: otelhttp.NewTransport(http.DefaultTransport)}

		// Call each downstream service with decremented depth
		for _, url := range downstreamURLs {
			url = strings.TrimSpace(url)
			if url == "" {
				continue
			}

			// Build the URL with decremented depth and propagate delay
			cascadeURL := url
			params := []string{
				"depth=" + strconv.Itoa(remainingDepth-1),
			}
			if hopDelay > 0 {
				params = append(params, "delay="+strconv.Itoa(hopDelay))
			}

			if !strings.Contains(url, "?") {
				cascadeURL += "?" + strings.Join(params, "&")
			} else {
				cascadeURL += "&" + strings.Join(params, "&")
			}

			slog.InfoContext(ctx, "Making cascade call", "url", cascadeURL, "depth", remainingDepth-1)

			req, err := http.NewRequestWithContext(ctx, "GET", cascadeURL, nil)
			if err != nil {
				slog.ErrorContext(ctx, "Failed to create cascade request", "url", cascadeURL, "error", err)
				continue
			}

			resp, err := client.Do(req)
			if err != nil {
				slog.ErrorContext(ctx, "Failed cascade call", "url", cascadeURL, "error", err)
				continue
			}
			resp.Body.Close()
		}
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Cascade OK"))
}

// --- Traffic Generator ---

func startTrafficGenerator() {
	client := http.Client{Transport: otelhttp.NewTransport(http.DefaultTransport)}

	errRate := getEnvAsInt("ERROR_RATE", 10)
	slowRate := getEnvAsInt("SLOW_RATE", 10)
	longJobRate := getEnvAsInt("LONG_JOB_RATE", 0)
	downstreams := os.Getenv("DOWNSTREAM_URLS")

	myPort := getEnv("APP_PORT", "8080")
	localBase := "http://localhost:" + myPort

	slog.Info("Traffic generator started",
		"downstream_enabled", downstreams != "",
		"long_job_rate", longJobRate)

	for {
		time.Sleep(time.Duration(100+rand.Intn(400)) * time.Millisecond)

		fullURL := ""

		if downstreams != "" {
			// If downstreams are configured, hit the downstream exactly as written
			urls := strings.Split(downstreams, ",")
			fullURL = strings.TrimSpace(urls[rand.Intn(len(urls))])
		} else {
			// Otherwise hit local endpoints based on probability
			path := "/fast"
			roll := rand.Intn(100)
			if roll < longJobRate {
				path = "/long-job"
			} else if roll < (longJobRate + errRate) {
				path = "/error"
			} else if roll < (longJobRate + errRate + slowRate) {
				path = "/slow"
			}
			fullURL = localBase + path
		}

		req, err := http.NewRequestWithContext(context.Background(), "GET", fullURL, nil)
		if err != nil {
			continue
		}

		resp, err := client.Do(req)
		if err != nil {
			slog.Debug("Traffic gen request failed", "url", fullURL, "error", err)
			continue
		}
		resp.Body.Close()
	}
}

// --- Helpers ---

func getEnv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}

func getEnvAsInt(key string, fallback int) int {
	strValue := getEnv(key, "")
	if strValue == "" {
		return fallback
	}
	value, err := strconv.Atoi(strValue)
	if err != nil {
		return fallback
	}
	return value
}
