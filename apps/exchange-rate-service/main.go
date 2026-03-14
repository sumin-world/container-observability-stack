package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8081"
	}

	mux := http.NewServeMux()

	// Health
	mux.HandleFunc("/health", healthHandler)

	// Rate endpoints — unsafe vs safe comparison
	mux.HandleFunc("/rate/unsafe/{currency}", unsafeRateHandler)
	mux.HandleFunc("/rate/safe/{currency}", safeRateHandler)

	// Exchange endpoints — race condition demo
	mux.HandleFunc("/exchange/unsafe", unsafeExchangeHandler)
	mux.HandleFunc("/exchange/safe", safeExchangeHandler)

	// Status & reset
	mux.HandleFunc("/status", statusHandler)
	mux.HandleFunc("/reset", resetExchangeHandler)

	// Prometheus metrics
	mux.Handle("/metrics", promhttp.Handler())

	// Wrap with instrumentation middleware
	handler := instrumentMiddleware(mux)

	server := &http.Server{
		Addr:         ":" + port,
		Handler:      handler,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Graceful shutdown
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		sig := <-sigCh
		log.Printf("Received %s, shutting down...", sig)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		server.Shutdown(ctx)
	}()

	log.Printf("exchange-rate-service starting on :%s", port)
	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatalf("Server error: %v", err)
	}
	log.Println("Server stopped")
}

// instrumentMiddleware records request metrics for every endpoint.
func instrumentMiddleware(next http.Handler) http.Handler {
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
		exchangeHttpRequestsTotal.WithLabelValues(r.Method, path, strconv.Itoa(sr.statusCode)).Inc()
		exchangeHttpRequestDuration.WithLabelValues(r.Method, path).Observe(duration.Seconds())
	})
}

type statusRecorder struct {
	http.ResponseWriter
	statusCode int
}

func (sr *statusRecorder) WriteHeader(code int) {
	sr.statusCode = code
	sr.ResponseWriter.WriteHeader(code)
}

func normalizePath(path string) string {
	switch {
	case len(path) >= 13 && path[:13] == "/rate/unsafe/":
		return "/rate/unsafe"
	case len(path) >= 11 && path[:11] == "/rate/safe/":
		return "/rate/safe"
	case path == "/exchange/unsafe":
		return path
	case path == "/exchange/safe":
		return path
	case path == "/health", path == "/status", path == "/reset":
		return path
	default:
		return "/other"
	}
}
