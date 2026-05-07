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
	h.Handler.Handle(ctx, r) // We ignore error here for brevity
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
	slog.Info("Starting server", "port", port)
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

func handleCascade(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	span := otrace.SpanFromContext(ctx)
	span.SetAttributes(attribute.String("handler.type", "cascade"))

	downstreamURLs := strings.Split(os.Getenv("DOWNSTREAM_URLS"), ",")
	callCount := getEnvAsInt("DOWNSTREAM_CALL_COUNT", 1)

	if len(downstreamURLs) > 0 && downstreamURLs[0] != "" {
		client := http.Client{Transport: otelhttp.NewTransport(http.DefaultTransport)}

		for i := 0; i < callCount; i++ {
			for _, url := range downstreamURLs {
				req, _ := http.NewRequestWithContext(ctx, "GET", strings.TrimSpace(url), nil)
				resp, err := client.Do(req)
				if err == nil {
					resp.Body.Close()
				}
			}
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
	downstreams := os.Getenv("DOWNSTREAM_URLS")

	myPort := getEnv("APP_PORT", "8080")
	localBase := "http://localhost:" + myPort

	slog.Info("Traffic generator started", "downstream_enabled", downstreams != "")

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
			if roll < errRate {
				path = "/error"
			} else if roll < (errRate + slowRate) {
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
