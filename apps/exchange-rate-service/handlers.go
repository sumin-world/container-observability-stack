package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"math/rand"
	"net/http"
	"sync"
	"time"
)

// --- Shared State ---

// previousRates stores the last known rate per currency.
// Protected by rateMu for thread-safe access.
var (
	previousRates = map[string]float64{
		"JPY": 934.0,
		"USD": 1350.0,
		"EUR": 1450.0,
	}
	rateMu sync.RWMutex
)

// breakers stores a circuit breaker per currency.
var breakers = map[string]*CircuitBreaker{
	"JPY": NewCircuitBreaker(3, 30*time.Second),
	"USD": NewCircuitBreaker(3, 30*time.Second),
	"EUR": NewCircuitBreaker(3, 30*time.Second),
}

// --- Data Source Simulation ---

// sourceA simulates a data provider that reports rates per 100 units.
// e.g., JPY: "100 JPY = 934 KRW" → returns 934.
func sourceA(currency string) float64 {
	base := map[string]float64{"JPY": 934.0, "USD": 1350.0, "EUR": 1450.0}
	b := base[currency]
	if b == 0 {
		b = 1000.0
	}
	return b + rand.Float64()*4 - 2 // normal jitter ±2
}

// sourceB simulates a data provider that reports rates per 1 unit.
// e.g., JPY: "1 JPY = 9.34 KRW" → returns 9.34.
//
// BUG: 5% chance it returns the per-1-unit price instead of per-100-unit.
// This simulates the unit mismatch that causes aggregation failures.
func sourceB(currency string) (float64, bool) {
	base := map[string]float64{"JPY": 934.0, "USD": 1350.0, "EUR": 1450.0}
	b := base[currency]
	if b == 0 {
		b = 1000.0
	}

	// 5% chance: return per-1-unit price (not normalized to per-100-unit)
	if rand.Float64() < 0.05 {
		return (b / 100) + rand.Float64()*0.04 - 0.02, true // ~9.34 for JPY — BUG!
	}
	return b + rand.Float64()*4 - 2, false // normal
}

// --- Endpoint Handlers ---

// unsafeRateHandler returns the exchange rate WITHOUT any validation.
//
// Failure modes demonstrated:
//   - No unit normalization between sources
//   - No divergence check between sources
//   - No rate-of-change validation
//   - Anomalous values propagate directly to consumers
func unsafeRateHandler(w http.ResponseWriter, r *http.Request) {
	currency := r.PathValue("currency")
	if currency == "" {
		currency = r.URL.Query().Get("currency")
	}
	if currency == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "currency required"})
		return
	}

	a := sourceA(currency)
	b, _ := sourceB(currency) // ignore the unit mismatch flag

	// BUG: naive average without unit normalization
	rate := (a + b) / 2.0

	// Record metrics (the anomaly will show up in dashboards)
	rateCurrent.WithLabelValues(currency).Set(rate)

	rateMu.RLock()
	prev := previousRates[currency]
	rateMu.RUnlock()

	change := 0.0
	if prev > 0 {
		change = math.Abs(rate-prev) / prev * 100
	}
	rateChangePercent.WithLabelValues(currency).Set(change)

	rateMu.Lock()
	previousRates[currency] = rate
	rateMu.Unlock()

	rateRequestsTotal.WithLabelValues(currency, "ok").Inc()

	writeJSON(w, http.StatusOK, map[string]any{
		"currency":   currency,
		"rate":       math.Round(rate*100) / 100,
		"source":     "unsafe",
		"change_pct": math.Round(change*100) / 100,
	})
}

// safeRateHandler returns the exchange rate WITH full validation.
//
// Defense layers:
//  1. Source divergence check (reject if sources differ > 10%)
//  2. Rate-of-change validation (reject if change > 30%)
//  3. Circuit breaker (stop serving if too many anomalies)
func safeRateHandler(w http.ResponseWriter, r *http.Request) {
	currency := r.PathValue("currency")
	if currency == "" {
		currency = r.URL.Query().Get("currency")
	}
	if currency == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "currency required"})
		return
	}

	// --- Circuit Breaker Check ---
	cb, ok := breakers[currency]
	if !ok {
		cb = NewCircuitBreaker(3, 30*time.Second)
		breakers[currency] = cb
	}

	// Update circuit breaker state metric
	circuitBreakerState.WithLabelValues(currency).Set(float64(cb.GetState()))

	if !cb.Allow() {
		rateRequestsTotal.WithLabelValues(currency, "circuit_open").Inc()
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error":               "circuit breaker open",
			"currency":            currency,
			"state":               cb.GetState().String(),
			"retry_after_seconds": 30,
		})
		return
	}

	// --- Fetch from both sources ---
	a := sourceA(currency)
	b, unitMismatch := sourceB(currency)

	// --- Layer 1: Source Divergence Check ---
	divergence := math.Abs(a-b) / math.Max(a, b) * 100
	sourceDivergencePercent.WithLabelValues(currency).Set(divergence)

	if divergence > 10 {
		cb.RecordFailure()
		circuitBreakerState.WithLabelValues(currency).Set(float64(cb.GetState()))
		rateRequestsTotal.WithLabelValues(currency, "source_divergence").Inc()

		log.Printf("BLOCKED: %s source divergence %.1f%% (A=%.2f B=%.2f mismatch=%v)",
			currency, divergence, a, b, unitMismatch)

		writeJSON(w, http.StatusConflict, map[string]any{
			"error":      "source divergence too high",
			"currency":   currency,
			"divergence": math.Round(divergence*100) / 100,
			"source_a":   math.Round(a*100) / 100,
			"source_b":   math.Round(b*100) / 100,
			"threshold":  10.0,
		})
		return
	}

	rate := (a + b) / 2.0

	// --- Layer 2: Rate-of-Change Validation ---
	rateMu.RLock()
	prev := previousRates[currency]
	rateMu.RUnlock()

	change := 0.0
	if prev > 0 {
		change = math.Abs(rate-prev) / prev * 100
	}
	rateChangePercent.WithLabelValues(currency).Set(change)

	if change > 30 {
		cb.RecordFailure()
		circuitBreakerState.WithLabelValues(currency).Set(float64(cb.GetState()))
		rateRequestsTotal.WithLabelValues(currency, "rate_change_rejected").Inc()

		log.Printf("BLOCKED: %s rate change %.1f%% (prev=%.2f new=%.2f)",
			currency, change, prev, rate)

		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"error":      "rate of change too large",
			"currency":   currency,
			"change_pct": math.Round(change*100) / 100,
			"previous":   math.Round(prev*100) / 100,
			"proposed":   math.Round(rate*100) / 100,
			"threshold":  30.0,
		})
		return
	}

	// --- All checks passed ---
	cb.RecordSuccess()
	circuitBreakerState.WithLabelValues(currency).Set(float64(cb.GetState()))

	rateMu.Lock()
	previousRates[currency] = rate
	rateMu.Unlock()

	rateCurrent.WithLabelValues(currency).Set(rate)
	rateRequestsTotal.WithLabelValues(currency, "ok").Inc()

	writeJSON(w, http.StatusOK, map[string]any{
		"currency":   currency,
		"rate":       math.Round(rate*100) / 100,
		"source":     "safe",
		"change_pct": math.Round(change*100) / 100,
		"divergence": math.Round(divergence*100) / 100,
		"cb_state":   cb.GetState().String(),
	})
}

// --- Concurrent Exchange Demo (Race Condition) ---

// Shared mutable state — intentionally unprotected for the unsafe path.
var (
	unsafeBalance float64 = 1000000 // 1M KRW
	safeBalance   float64 = 1000000
	balanceMu     sync.Mutex
)

// unsafeExchangeHandler demonstrates a TOCTOU race condition.
//
// The bug: we read the rate, sleep (simulating processing), then use the rate.
// Between read and use, another goroutine can change the rate.
// With `go test -race`, this will trigger the race detector.
func unsafeExchangeHandler(w http.ResponseWriter, r *http.Request) {
	currency := "JPY"

	// --- READ (Time-of-Check) ---
	rate := previousRates[currency] // NO LOCK — race condition!

	// Simulate processing delay where rate could change
	time.Sleep(time.Duration(rand.Intn(10)) * time.Millisecond)

	// --- USE (Time-of-Use) ---
	actualRate := previousRates[currency] // read again, also no lock

	amount := 10000.0 // buy 10,000 JPY
	cost := amount / 100 * rate

	unsafeBalance -= cost // NO LOCK — another race!

	// Detect if rate changed between check and use
	if math.Abs(rate-actualRate) > 0.01 {
		raceConditionDetected.Inc()
		log.Printf("RACE DETECTED: read=%.2f actual=%.2f diff=%.2f",
			rate, actualRate, math.Abs(rate-actualRate))
	}

	exchangeExecutedTotal.WithLabelValues(currency, "unsafe").Inc()

	writeJSON(w, http.StatusOK, map[string]any{
		"type":          "unsafe",
		"currency":      currency,
		"rate_at_check": math.Round(rate*100) / 100,
		"rate_at_use":   math.Round(actualRate*100) / 100,
		"amount_jpy":    amount,
		"cost_krw":      math.Round(cost*100) / 100,
		"balance_krw":   math.Round(unsafeBalance*100) / 100,
	})
}

// safeExchangeHandler demonstrates the thread-safe version.
//
// Fix: use mutex to ensure rate read and balance update are atomic.
// Double-check pattern: verify rate is still acceptable after acquiring lock.
func safeExchangeHandler(w http.ResponseWriter, r *http.Request) {
	currency := "JPY"

	// Read rate under read lock
	rateMu.RLock()
	rate := previousRates[currency]
	rateMu.RUnlock()

	amount := 10000.0

	// Acquire balance lock for atomic check-and-update
	balanceMu.Lock()
	defer balanceMu.Unlock()

	// Double-check: re-read rate under lock to prevent TOCTOU
	rateMu.RLock()
	confirmedRate := previousRates[currency]
	rateMu.RUnlock()

	// If rate changed significantly between reads, abort
	if math.Abs(rate-confirmedRate)/rate > 0.01 { // > 1% change
		exchangeExecutedTotal.WithLabelValues(currency, "aborted").Inc()
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":          "rate changed during processing",
			"initial_rate":   fmt.Sprintf("%.2f", rate),
			"confirmed_rate": fmt.Sprintf("%.2f", confirmedRate),
		})
		return
	}

	cost := amount / 100 * confirmedRate

	if safeBalance < cost {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"error":   "insufficient balance",
			"balance": safeBalance,
			"cost":    cost,
		})
		return
	}

	safeBalance -= cost
	exchangeExecutedTotal.WithLabelValues(currency, "safe").Inc()

	writeJSON(w, http.StatusOK, map[string]any{
		"type":        "safe",
		"currency":    currency,
		"rate":        math.Round(confirmedRate*100) / 100,
		"amount_jpy":  amount,
		"cost_krw":    math.Round(cost*100) / 100,
		"balance_krw": math.Round(safeBalance*100) / 100,
	})
}

// statusHandler returns the current state of all circuit breakers and balances.
func statusHandler(w http.ResponseWriter, r *http.Request) {
	cbStates := map[string]string{}
	for cur, cb := range breakers {
		cbStates[cur] = cb.GetState().String()
	}

	rateMu.RLock()
	rates := make(map[string]float64, len(previousRates))
	for k, v := range previousRates {
		rates[k] = math.Round(v*100) / 100
	}
	rateMu.RUnlock()

	balanceMu.Lock()
	sb := safeBalance
	balanceMu.Unlock()

	writeJSON(w, http.StatusOK, map[string]any{
		"rates":            rates,
		"circuit_breakers": cbStates,
		"unsafe_balance":   math.Round(unsafeBalance*100) / 100,
		"safe_balance":     math.Round(sb*100) / 100,
	})
}

// resetExchangeHandler resets all state to initial values.
func resetExchangeHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "use POST"})
		return
	}

	rateMu.Lock()
	previousRates = map[string]float64{"JPY": 934.0, "USD": 1350.0, "EUR": 1450.0}
	rateMu.Unlock()

	balanceMu.Lock()
	unsafeBalance = 1000000
	safeBalance = 1000000
	balanceMu.Unlock()

	for _, cb := range breakers {
		cb.RecordSuccess() // reset to closed
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "reset complete"})
}

// --- Helpers ---

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func healthHandler(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "healthy"})
}
