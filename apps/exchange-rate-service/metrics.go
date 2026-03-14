package main

import "github.com/prometheus/client_golang/prometheus"

// --- Prometheus Metrics ---

var (
	rateRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "exchange_rate_requests_total",
			Help: "Total exchange rate requests by currency and status.",
		},
		[]string{"currency", "status"},
	)

	rateCurrent = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "exchange_rate_current",
			Help: "Current exchange rate value per currency.",
		},
		[]string{"currency"},
	)

	rateChangePercent = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "exchange_rate_change_percent",
			Help: "Rate of change percent from previous value.",
		},
		[]string{"currency"},
	)

	sourceDivergencePercent = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "exchange_rate_source_divergence_percent",
			Help: "Divergence between data sources before aggregation.",
		},
		[]string{"currency"},
	)

	circuitBreakerState = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "exchange_rate_circuit_breaker_state",
			Help: "Circuit breaker state: 0=closed, 1=open, 2=half-open.",
		},
		[]string{"currency"},
	)

	exchangeExecutedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "exchange_executed_total",
			Help: "Total exchange executions by currency and result.",
		},
		[]string{"currency", "result"},
	)

	raceConditionDetected = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "exchange_race_condition_detected_total",
			Help: "Number of race conditions detected in unsafe exchange path.",
		},
	)

	exchangeHttpRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "exchange_http_requests_total",
			Help: "Total HTTP requests to exchange-rate-service by method, path, and status.",
		},
		[]string{"method", "path", "status"},
	)

	exchangeHttpRequestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "exchange_http_request_duration_seconds",
			Help:    "HTTP request duration in seconds for exchange-rate-service.",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"method", "path"},
	)
)

func init() {
	prometheus.MustRegister(rateRequestsTotal)
	prometheus.MustRegister(rateCurrent)
	prometheus.MustRegister(rateChangePercent)
	prometheus.MustRegister(sourceDivergencePercent)
	prometheus.MustRegister(circuitBreakerState)
	prometheus.MustRegister(exchangeExecutedTotal)
	prometheus.MustRegister(raceConditionDetected)
	prometheus.MustRegister(exchangeHttpRequestsTotal)
	prometheus.MustRegister(exchangeHttpRequestDuration)
}
