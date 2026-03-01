# INC-001 — Memory Leak in go-api

| Field          | Value                             |
|---------------|-----------------------------------|
| Severity      | SEV-2                             |
| Status        | Resolved                          |
| Duration      | ~25 minutes                       |
| Affected      | go-api service                    |
| Detected by   | Grafana alert (MemoryLeakSuspected) |

## Timeline

| Time (UTC)  | Event                                                         |
|-------------|---------------------------------------------------------------|
| 14:00       | Leak simulation started (`simulate-leak.sh 50 2`)            |
| 14:02       | `heap_alloc_bytes` crosses 50 MB                             |
| 14:05       | MemoryLeakSuspected alert fires (heap > 100 MB for 5 min)   |
| 14:08       | On-call acknowledges alert                                    |
| 14:10       | Pprof heap profile captured: `go tool pprof http://localhost:8080/debug/pprof/heap` |
| 14:12       | Root cause identified: `leakHandler` appending to `leakyStore` without release |
| 14:15       | Fix applied: `curl -X POST http://localhost:8080/reset`      |
| 14:17       | `heap_alloc_bytes` returns to baseline (~8 MB)               |
| 14:25       | Monitoring confirms stable memory for 10 min — incident closed |

## Root Cause

The `/leak` endpoint allocates a 1 MB byte slice on each request and appends it
to a package-level slice (`leakyStore`). This slice is never garbage-collected
because it remains reachable. Repeated requests cause unbounded heap growth.

## Resolution

Immediate: Called `/reset` endpoint (POST) which nils out `leakyStore` and
triggers `runtime.GC()`.

Long-term: The leak endpoint exists intentionally for observability demos. The
safety cap (200 chunks / ~200 MB) prevents actual OOM. In a real service, the
equivalent fix would be identifying and removing the unbounded cache or adding
an eviction policy.

## Lessons Learned

- Prometheus `heap_alloc_bytes` gauge + alert rule caught the leak within 5 minutes.
- Having `/debug/pprof/heap` exposed allowed immediate root-cause identification.
- The `/reset` endpoint provided a one-command mitigation without restart.

## Prevention Checklist

- [x] Alert rule for heap growth (`MemoryLeakSuspected`)
- [x] Container memory limit set (`mem_limit: 256m`)
- [x] Pprof endpoints exposed for profiling
- [x] Safety cap on leak endpoint (200 chunks)
- [ ] Automated canary that detects monotonic memory growth in CI
