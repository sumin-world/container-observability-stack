# Runbook — Memory Growth Investigation

## When to Use

This runbook applies when any of these alerts fire:

- **MemoryLeakSuspected** — `heap_alloc_bytes > 100 MB` for 5 min
- **ContainerMemoryHigh** — container memory > 85 % of limit for 2 min

## Quick Fix

**Option A — Reset leaked memory (non-disruptive):**

```bash
curl -X POST http://localhost:8080/reset
```

This clears the in-memory leak store and triggers `runtime.GC()`. Verify in
Grafana that `heap_alloc_bytes` drops back to baseline.

**Option B — Restart the container:**

```bash
docker compose restart go-api
```

## Investigation Steps

### 1. Confirm the Trend

Open Grafana → **Go API Observability** dashboard.

Check:
- Container Memory Usage panel — is it monotonically increasing?
- Heap Allocation panel — does `heap_alloc_bytes` grow without GC recovery?
- Leaked Chunks panel — is it non-zero?

### 2. Capture a Heap Profile

```bash
# Snapshot 1
curl -o heap1.pb.gz http://localhost:8080/debug/pprof/heap

# Wait 2–5 minutes, then snapshot 2
curl -o heap2.pb.gz http://localhost:8080/debug/pprof/heap

# Compare (differential profile)
go tool pprof -http=:9999 -diff_base=heap1.pb.gz heap2.pb.gz
```

Look for allocations that appear only in the diff — these are the objects
accumulating between snapshots.

### 3. Check Goroutine Count

```bash
curl http://localhost:8080/debug/pprof/goroutine?debug=1
```

A growing goroutine count may indicate leaked goroutines holding references.

### 4. Check Prometheus Metrics

```promql
# Heap growth rate (bytes/second over 10 min)
rate(heap_alloc_bytes[10m])

# Leaked chunks count
leaked_chunks_total

# Container memory vs limit
container_memory_usage_bytes{name="go-api"} / 268435456
```

## Escalation

If the leak persists after `/reset` and container restart:

1. Capture a full pprof heap dump and save it for offline analysis.
2. Check recent deployments for new allocations or changed cache behaviour.
3. Escalate to the service owner with the differential pprof output.

## Related

- [INC-001 — Memory Leak](../incidents/INC-001-memory-leak.md)
- Grafana dashboard: Go API Observability
- Alert rules: `infra/prometheus/alerts.yml`
