# Container Observability Stack

A complete container observability lab built around a Go HTTP service that
simulates real-world failure modes — memory leaks, latency spikes, and error
bursts. The stack wires **Prometheus**, **Grafana**, **OpenTelemetry Collector**,
**cAdvisor**, and **Node Exporter** together so you can practise incident
detection and response with production-grade tooling.

## Architecture

```
┌──────────┐      ┌───────────────────┐      ┌────────────┐
│  go-api  │─────▶│  OTel Collector   │─────▶│ Prometheus │
│ :8080    │      │ :4317 / :4318     │      │ :9090      │
└──────────┘      └───────────────────┘      └─────┬──────┘
     │                                              │
     │            ┌───────────────────┐             │
     └───────────▶│    cAdvisor       │─────────────┘
                  │    :8081          │             │
                  └───────────────────┘             │
                  ┌───────────────────┐       ┌─────▼──────┐
                  │  Node Exporter    │──────▶│  Grafana   │
                  │  :9100            │       │  :3000     │
                  └───────────────────┘       └────────────┘
```

## Quick Start

```bash
docker compose up -d --build
```

| Service        | URL                          |
|---------------|------------------------------|
| Go API        | http://localhost:8080         |
| Prometheus    | http://localhost:9090         |
| Grafana       | http://localhost:3000         |
| cAdvisor      | http://localhost:8081         |
| Node Exporter | http://localhost:9100/metrics |

Default Grafana credentials: `admin` / `admin`

## API Endpoints

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

## Simulating an Incident

```bash
# Generate a memory leak over 50 requests
./scripts/simulate-leak.sh 50 2

# Observe in Grafana → Go API Observability dashboard
# Resolve with:
curl -X POST http://localhost:8080/reset
```

## Key Design Decisions

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

## Alert Rules

| Alert                 | Condition                           | Severity |
|-----------------------|-------------------------------------|----------|
| HighErrorRate         | 5xx / total > 10 % for 2 min       | warning  |
| HighP99Latency        | P99 > 500 ms for 3 min             | warning  |
| MemoryLeakSuspected   | Heap > 100 MB for 5 min            | critical |
| ContainerMemoryHigh   | Container > 85 % of 256 MB for 2 m | critical |

## Project Structure

```
.
├── apps/go-api/          # Go HTTP service with Prometheus instrumentation
│   ├── main.go
│   ├── go.mod
│   └── Dockerfile
├── infra/
│   ├── prometheus/       # Prometheus config + alert rules
│   ├── otel-collector/   # OpenTelemetry Collector config
│   └── grafana/          # Datasource + dashboard provisioning
├── scripts/              # Leak simulation script
├── docs/
│   ├── incidents/        # Incident reports (INC-001)
│   └── runbooks/         # Investigation runbooks
├── docker-compose.yml
└── README.md
```

## Teardown

```bash
docker compose down -v
```
