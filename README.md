# Container Observability Stack

A hands-on observability lab built with Go microservices and production-grade
monitoring tools. The stack simulates real-world failure modes — memory leaks,
latency spikes, error bursts, data pipeline anomalies, and concurrency bugs — so
you can practise incident detection and response end-to-end.

## Architecture

```
┌──────────────┐    ┌──────────────────────┐    ┌────────────┐
│   go-api     │───▶│   OTel Collector     │───▶│ Prometheus │
│   :8080      │    │   :4317 / :4318      │    │   :9090    │
└──────┬───────┘    └──────────────────────┘    └─────┬──────┘
       │                                              │
┌──────┴───────┐    ┌──────────────────────┐          │
│  exchange-   │───▶│     cAdvisor         │──────────┤
│  rate-service│    │     :8081            │          │
│  :8082       │    └──────────────────────┘          │
└──────────────┘    ┌──────────────────────┐    ┌─────▼──────┐
                    │   Node Exporter      │───▶│  Grafana   │
                    │   :9100              │    │   :3000    │
                    └──────────────────────┘    └────────────┘
```

## Quick Start

```bash
docker compose up -d --build
```

| Service               | URL                          |
|-----------------------|------------------------------|
| Go API                | http://localhost:8080         |
| Exchange Rate Service | http://localhost:8082         |
| Prometheus            | http://localhost:9090         |
| Grafana               | http://localhost:3000         |
| cAdvisor              | http://localhost:8081         |
| Node Exporter         | http://localhost:9100/metrics |

Default Grafana credentials: `admin` / `admin`

---

## Services

### go-api — Failure Simulation Service

A Go HTTP service that simulates common container failure modes for observability
testing: memory leaks, latency spikes, and error bursts. Instrumented with
Prometheus client and Go pprof for profiling.

#### Endpoints

| Endpoint              | Method       | Description                                    |
|-----------------------|-------------|------------------------------------------------|
| `/`                   | GET         | Service info with uptime                       |
| `/health`             | GET         | Liveness probe                                 |
| `/ready`              | GET         | Readiness probe                                |
| `/leak`               | GET         | Allocate ~1 MB that is never freed             |
| `/slow`               | GET         | Random 200–1000 ms delay                       |
| `/error`              | GET         | Returns 500 with ~40 % probability             |
| `/reset`              | POST/DELETE | Free all leaked memory and trigger GC          |
| `/metrics`            | GET         | Prometheus metrics (auto-instrumented)         |
| `/debug/pprof/`       | GET         | Go pprof index                                 |
| `/debug/pprof/heap`   | GET         | Heap profile                                   |

#### Key Design Decisions

- **`prometheus/client_golang`** — Native Prometheus instrumentation exposes
  `http_requests_total` (CounterVec) and `http_request_duration_seconds`
  (HistogramVec) per method, path, and status code.
- **Path normalisation** — Endpoint labels are collapsed to a known set to
  prevent high-cardinality label explosion in Prometheus.
- **`statusRecorder` middleware** — Wraps `http.ResponseWriter` to capture the
  status code written by downstream handlers without buffering the body.
- **Memory safety cap** — `/leak` stops allocating after 200 chunks (~200 MB)
  to avoid killing the container before you can observe the trend.
- **Container memory limit** — `mem_limit: 256m` in Compose mirrors a
  production constraint so cAdvisor shows realistic pressure.
- **Multi-stage Docker build** — `go mod tidy` runs at build time so the image
  builds without a local Go toolchain.

### exchange-rate-service — Data Pipeline Resilience Demo

A deliberately vulnerable exchange rate service that demonstrates common failure
modes in data pipelines that aggregate from multiple sources. Each failure mode
has an **unsafe** (vulnerable) and **safe** (hardened) endpoint so you can
compare behaviour side by side in Grafana.

#### Endpoints

| Endpoint                    | Method | Description                                              |
|-----------------------------|--------|----------------------------------------------------------|
| `/rate/unsafe/{currency}`   | GET    | Returns rate WITHOUT validation — anomalies pass through |
| `/rate/safe/{currency}`     | GET    | Returns rate WITH divergence check + rate-of-change + circuit breaker |
| `/exchange/unsafe`          | GET    | Executes exchange with TOCTOU race condition              |
| `/exchange/safe`            | GET    | Thread-safe exchange with double-check pattern            |
| `/status`                   | GET    | Current rates, circuit breaker states, balances           |
| `/reset`                    | POST   | Reset all state to initial values                        |
| `/health`                   | GET    | Liveness probe                                           |
| `/metrics`                  | GET    | Prometheus metrics                                       |

#### Failure Scenarios

1. **Multi-Source Unit Mismatch** — Source B has a 5% chance of returning a
   per-1-unit price instead of per-100-unit, causing ~50% rate drops when
   naively averaged. The safe endpoint catches this via source divergence check.

2. **Rate-of-Change Spike** — When an anomalous value is accepted (unsafe
   path), subsequent requests see a massive rate-of-change. The safe endpoint
   rejects changes > 30%.

3. **Circuit Breaker Activation** — After 3 consecutive anomalies on the safe
   path, the circuit breaker trips to OPEN state, rejecting all requests for
   30 seconds.

4. **TOCTOU Race Condition** — `/exchange/unsafe` reads the rate and updates
   the balance without locks, creating a Time-of-Check to Time-of-Use bug
   under concurrent requests. `/exchange/safe` uses `sync.Mutex` + double-check
   pattern.

#### Prometheus Metrics

| Metric                                       | Type    | Description                             |
|----------------------------------------------|---------|-----------------------------------------|
| `exchange_rate_current`                      | Gauge   | Current rate per currency               |
| `exchange_rate_change_percent`               | Gauge   | Rate of change from previous value      |
| `exchange_rate_source_divergence_percent`    | Gauge   | Divergence between data sources         |
| `exchange_rate_circuit_breaker_state`        | Gauge   | 0=closed, 1=open, 2=half-open          |
| `exchange_rate_requests_total`               | Counter | Requests by currency and status         |
| `exchange_executed_total`                    | Counter | Exchanges by currency and result        |
| `exchange_race_condition_detected_total`     | Counter | Race conditions in unsafe path          |

---

## Simulating Incidents

### Memory Leak (go-api)

```bash
# Generate a memory leak over 50 requests
./scripts/simulate-leak.sh 50 2

# Observe in Grafana → Go API Observability dashboard
# Resolve with:
curl -X POST http://localhost:8080/reset
```

### Exchange Rate Anomaly (exchange-rate-service)

```bash
# Compare unsafe vs safe endpoints under anomaly conditions
./scripts/simulate-exchange-anomaly.sh 100 0.5

# Observe in Grafana → Exchange Rate Service — Resilience dashboard
# Reset state:
curl -X POST http://localhost:8082/reset
```

---

## Alert Rules

### go-api Alerts

| Alert                 | Condition                           | Severity |
|-----------------------|-------------------------------------|----------|
| HighErrorRate         | 5xx / total > 10 % for 2 min       | warning  |
| HighP99Latency        | P99 > 500 ms for 3 min             | warning  |
| MemoryLeakSuspected   | Heap > 100 MB for 5 min            | critical |
| ContainerMemoryHigh   | Container > 85 % of 256 MB for 2 m | critical |

### exchange-rate-service Alerts

| Alert                       | Condition                                  | Severity |
|-----------------------------|--------------------------------------------|----------|
| ExchangeRateAnomalyDetected | Rate of change > 30 %                     | critical |
| SourceDivergenceHigh        | Source divergence > 10 %                   | critical |
| CircuitBreakerOpen          | Circuit breaker state = OPEN               | warning  |
| RaceConditionDetected       | Race conditions detected in unsafe path    | warning  |

---

## Project Structure

```
.
├── apps/
│   ├── go-api/                    # Failure simulation service
│   │   ├── main.go                # Endpoints, middleware, Prometheus metrics
│   │   ├── go.mod
│   │   └── Dockerfile
│   └── exchange-rate-service/     # Data pipeline resilience demo
│       ├── main.go                # Server, routing, path normalisation
│       ├── handlers.go            # Rate/exchange handlers, race condition demo
│       ├── metrics.go             # Prometheus metric definitions
│       ├── circuit_breaker.go     # Circuit breaker implementation
│       ├── go.mod
│       ├── Dockerfile
│       └── README.md
├── infra/
│   ├── prometheus/                # Prometheus config + alert rules
│   │   ├── prometheus.yml
│   │   └── alerts.yml
│   ├── otel-collector/            # OpenTelemetry Collector config
│   │   └── config.yml
│   └── grafana/                   # Datasource + dashboard provisioning
│       └── provisioning/
│           ├── datasources/
│           └── dashboards/
├── scripts/
│   ├── simulate-leak.sh           # Memory leak simulation
│   └── simulate-exchange-anomaly.sh  # Exchange rate anomaly simulation
├── docs/
│   ├── incidents/                 # Incident reports (INC-001)
│   ├── runbooks/                  # Investigation runbooks
│   └── case-studies/              # Data pipeline failure analysis
├── .github/workflows/ci.yml      # CI: Go tests, Docker builds, config validation
├── docker-compose.yml
└── README.md
```

## Grafana Dashboards

Two pre-provisioned dashboards are available at http://localhost:3000:

- **Go API Observability** — Container memory, heap allocation, leaked chunks,
  request rate, error rate, and latency percentiles.
- **Exchange Rate Service — Resilience** — Current rates, rate-of-change,
  source divergence, circuit breaker state, and race condition counter.

## CI Pipeline

GitHub Actions runs on every push and PR to `main`:

1. **Go tests** — `go vet` and `go test -race` for go-api; build verification
   for exchange-rate-service.
2. **Docker builds** — Verifies both service images build successfully.
3. **Config validation** — Runs `promtool check config` and
   `promtool check rules` against Prometheus configuration.

## Teardown

```bash
docker compose down -v
```
