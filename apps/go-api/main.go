package main

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"net/http/pprof"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const (
	defaultPort     = "8080"
	chunkSize       = 1 << 20 // 1MB per leak call
	maxLeakChunks   = 200     // safety cap to prevent actual OOM
	slowMinDelayMs  = 200
	slowMaxRangeMs  = 800
	errorRate       = 0.4
	shutdownTimeout = 5 * time.Second
	metricsInterval = 10 * time.Second
)

var (
	leakyStore [][]byte
	mu         sync.Mutex
	startTime  = time.Now()
)

// Prometheus metrics
var (
	httpRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "Total number of HTTP requests by method, path, and status code.",
		},
		[]string{"method", "path", "status"},
	)

	httpRequestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "HTTP request duration in seconds.",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"method", "path"},
	)

	leakedChunksGauge = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "leaked_chunks_total",
			Help: "Current number of leaked memory chunks in the store.",
		},
	)

	heapAllocBytes = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "heap_alloc_bytes",
			Help: "Current heap allocation in bytes from runtime.MemStats.",
		},
	)
)

func init() {
	prometheus.MustRegister(httpRequestsTotal)
	prometheus.MustRegister(httpRequestDuration)
	prometheus.MustRegister(leakedChunksGauge)
	prometheus.MustRegister(heapAllocBytes)
}

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = defaultPort
	}

	mux := http.NewServeMux()

	// Core endpoints
	mux.HandleFunc("/health", healthHandler)
	mux.HandleFunc("/ready", readyHandler)
	mux.HandleFunc("/", rootHandler)

	// Observability demo endpoints
	mux.HandleFunc("/leak", leakHandler)
	mux.HandleFunc("/slow", slowHandler)
	mux.HandleFunc("/error", errorHandler)
	mux.HandleFunc("/reset", resetHandler)

	// Prometheus metrics endpoint
	mux.Handle("/metrics", promhttp.Handler())

	// pprof endpoints for memory profiling
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	mux.HandleFunc("/debug/pprof/heap", pprof.Handler("heap").ServeHTTP)
	mux.HandleFunc("/debug/pprof/goroutine", pprof.Handler("goroutine").ServeHTTP)

	// Publish Go runtime metrics to Prometheus periodically
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go publishRuntimeMetrics(ctx)

	server := &http.Server{
		Addr:         ":" + port,
		Handler:      instrumentedMiddleware(mux),
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Graceful shutdown on SIGINT / SIGTERM
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		sig := <-sigCh
		log.Printf("Received %s, shutting down gracefully...", sig)
		cancel()

		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer shutdownCancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			log.Printf("Shutdown error: %v", err)
		}
	}()

	log.Printf("go-api starting on :%s", port)
	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatalf("Server error: %v", err)
	}
	log.Println("Server stopped gracefully")
}

// --- Handlers ---

func rootHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, http.StatusOK, `{"service":"go-api","status":"running","uptime":"%s"}`,
		time.Since(startTime).Round(time.Second))
}

func healthHandler(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, `{"status":"healthy"}`)
}

func readyHandler(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, `{"status":"ready"}`)
}

// leakHandler allocates ~1MB per call and never frees it.
func leakHandler(w http.ResponseWriter, _ *http.Request) {
	mu.Lock()
	defer mu.Unlock()

	if len(leakyStore) >= maxLeakChunks {
		writeJSON(w, http.StatusServiceUnavailable,
			`{"error":"safety cap reached","max_chunks":%d,"heap_alloc_mb":%.1f}`,
			maxLeakChunks, heapAllocMB())
		return
	}

	chunk := make([]byte, chunkSize)
	for i := range chunk {
		chunk[i] = byte(rand.Intn(256))
	}
	leakyStore = append(leakyStore, chunk)
	leakedChunksGauge.Set(float64(len(leakyStore)))

	writeJSON(w, http.StatusOK,
		`{"leaked_chunks":%d,"heap_alloc_mb":%.1f,"sys_mb":%.1f}`,
		len(leakyStore), heapAllocMB(), sysMB())
	log.Printf("LEAK: chunks=%d heap=%.1fMB", len(leakyStore), heapAllocMB())
}

// resetHandler clears the leaky store for incident recovery.
func resetHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodDelete {
		writeJSON(w, http.StatusMethodNotAllowed, `{"error":"use POST or DELETE method"}`)
		return
	}

	mu.Lock()
	before := len(leakyStore)
	leakyStore = nil
	leakedChunksGauge.Set(0)
	mu.Unlock()

	runtime.GC()

	writeJSON(w, http.StatusOK,
		`{"cleared_chunks":%d,"heap_alloc_mb":%.1f}`, before, heapAllocMB())
	log.Printf("RESET: cleared %d chunks, heap=%.1fMB", before, heapAllocMB())
}

// slowHandler adds artificial latency to simulate slow queries.
func slowHandler(w http.ResponseWriter, _ *http.Request) {
	delay := time.Duration(slowMinDelayMs+rand.Intn(slowMaxRangeMs)) * time.Millisecond
	time.Sleep(delay)
	writeJSON(w, http.StatusOK, `{"delay_ms":%d}`, delay.Milliseconds())
}

// errorHandler returns 500 with ~40% probability to simulate error spikes.
func errorHandler(w http.ResponseWriter, _ *http.Request) {
	if rand.Float64() < errorRate {
		writeJSON(w, http.StatusInternalServerError, `{"error":"simulated internal server error"}`)
		return
	}
	writeJSON(w, http.StatusOK, `{"status":"ok"}`)
}

// --- Helpers ---

func writeJSON(w http.ResponseWriter, status int, format string, args ...any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	fmt.Fprintf(w, format, args...)
}

func heapAllocMB() float64 {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return float64(m.HeapAlloc) / 1024 / 1024
}

func sysMB() float64 {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return float64(m.Sys) / 1024 / 1024
}

func publishRuntimeMetrics(ctx context.Context) {
	ticker := time.NewTicker(metricsInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			var m runtime.MemStats
			runtime.ReadMemStats(&m)
			heapAllocBytes.Set(float64(m.HeapAlloc))
		}
	}
}

// --- Middleware ---

type statusRecorder struct {
	http.ResponseWriter
	statusCode int
}

func (sr *statusRecorder) WriteHeader(code int) {
	sr.statusCode = code
	sr.ResponseWriter.WriteHeader(code)
}

func instrumentedMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/metrics" {
			next.ServeHTTP(w, r)
			return
		}

		start := time.Now()
		sr := &statusRecorder{ResponseWriter: w, statusCode: http.StatusOK}
		next.ServeHTTP(sr, r)
		duration := time.Since(start)

		path := normalizePath(r.URL.Path)
		httpRequestsTotal.WithLabelValues(r.Method, path, strconv.Itoa(sr.statusCode)).Inc()
		httpRequestDuration.WithLabelValues(r.Method, path).Observe(duration.Seconds())

		log.Printf("%s %s %d %s", r.Method, r.URL.Path, sr.statusCode, duration.Round(time.Microsecond))
	})
}

func normalizePath(path string) string {
	switch path {
	case "/", "/health", "/ready", "/leak", "/slow", "/error", "/reset":
		return path
	}
	if len(path) >= 13 && path[:13] == "/debug/pprof/" {
		return "/debug/pprof"
	}
	return "/other"
}
