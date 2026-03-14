# Exchange Rate Service — Resilience & Observability Demo

A deliberately vulnerable exchange rate service that demonstrates common
failure modes in financial data pipelines and their mitigations.

## Endpoints

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/rate/unsafe/{currency}` | GET | Returns rate WITHOUT validation — anomalies pass through |
| `/rate/safe/{currency}` | GET | Returns rate WITH divergence check + rate-of-change + circuit breaker |
| `/exchange/unsafe` | GET | Executes exchange with TOCTOU race condition |
| `/exchange/safe` | GET | Thread-safe exchange with double-check pattern |
| `/status` | GET | Current rates, circuit breaker states, balances |
| `/reset` | POST | Reset all state to initial values |
| `/health` | GET | Liveness probe |
| `/metrics` | GET | Prometheus metrics |

## Failure Scenarios

### 1. Multi-Source Unit Mismatch
The service aggregates rates from two simulated data sources. Source B has
a 5% chance of returning a per-1-unit price instead of per-100-unit, causing
~50% rate drops when naively averaged.

- **Unsafe endpoint:** anomaly passes through silently
- **Safe endpoint:** source divergence check catches it

### 2. Rate-of-Change Spike
When an anomalous value is accepted (unsafe path), subsequent requests see
a massive rate-of-change that should trigger alerts.

- **Unsafe endpoint:** no rate-of-change validation
- **Safe endpoint:** rejects changes > 30%

### 3. Circuit Breaker Activation
After 3 consecutive anomalies on the safe path, the circuit breaker trips
to OPEN state, rejecting all requests for 30 seconds.

### 4. TOCTOU Race Condition
The `/exchange/unsafe` endpoint reads the rate and updates the balance
without locks, creating a Time-of-Check to Time-of-Use bug under
concurrent requests.

- **Unsafe endpoint:** no synchronization → race condition
- **Safe endpoint:** `sync.Mutex` + double-check pattern

## Prometheus Metrics

| Metric | Type | Description |
|--------|------|-------------|
| `exchange_rate_current` | Gauge | Current rate per currency |
| `exchange_rate_change_percent` | Gauge | Rate of change from previous value |
| `exchange_rate_source_divergence_percent` | Gauge | Divergence between data sources |
| `exchange_rate_circuit_breaker_state` | Gauge | 0=closed, 1=open, 2=half-open |
| `exchange_rate_requests_total` | Counter | Requests by currency and status |
| `exchange_executed_total` | Counter | Exchanges by currency and result |
| `exchange_race_condition_detected_total` | Counter | Race conditions in unsafe path |

## Running

```bash
# From project root
docker compose up -d

# Simulate anomalies
./scripts/simulate-exchange-anomaly.sh

# Check results
open http://localhost:3000  # Grafana → Exchange Rate Service — Resilience

# Reset state
curl -X POST http://localhost:8082/reset
```

## Design Motivation

Financial data pipelines consuming external pricing feeds are vulnerable to:

1. **Data quality failures** — upstream sources returning values in
   unexpected units
2. **Cascading automation** — bad data triggering automated transactions
   at incorrect prices
3. **Concurrency bugs** — TOCTOU gaps between rate validation and
   transaction execution under load

This service recreates each scenario in isolation so they can be detected,
alerted on, and resolved using the observability stack.
