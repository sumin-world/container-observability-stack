# INC-001 — Memory Leak in go-api (Re-test, 2026-10-11)

> The first version of this report described a timeline that could not happen:
> `simulate-leak.sh 50 2` leaks ~50 MB, which never reaches the 100 MB alert
> threshold. This version records an actual run on Docker Desktop (macOS).
> All times are Prometheus timestamps (UTC).

| Field          | Value                                                   |
|----------------|---------------------------------------------------------|
| Severity       | SEV-2 (simulated)                                       |
| Status         | Resolved                                                |
| Affected       | go-api service                                          |
| Detected by    | Prometheus alert state (`MemoryLeakSuspected`, `HighErrorRate`) — no Alertmanager is configured, so nothing is routed to a person |
| Environment    | Docker Desktop on macOS, Prometheus v2.50.1, `mem_limit: 256m` |

## Measured Values

| Observation                          | Value                                   |
|--------------------------------------|-----------------------------------------|
| Heap at start                        | ~1 MB (not 8 MB as previously written)  |
| Heap after 50 requests               | 53.0 MB — below the 100 MB threshold    |
| Request that crosses 100 MB          | 98th (100.4 MB), reproduced in two runs |
| Safety cap                           | 200 chunks → HTTP 503 on further `/leak` calls |
| Container memory at cap              | 213.6 MiB / 256 MiB (83 %)              |
| pprof `inuse_space` top              | `main.leakHandler` — 128.37 MB, 99.61 % |
| Heap after `/reset`                  | 2.8 MB                                  |
| Go `Sys` memory after `/reset`       | 218.1 MB — unchanged                    |

## Timeline (Run 1: 120 + 120 requests, 1 s apart)

| Time (UTC) | Event                                                                 |
|------------|-----------------------------------------------------------------------|
| ~07:04:45  | `simulate-leak.sh 120 1` started                                      |
| 07:06:21   | `MemoryLeakSuspected` → **pending** (heap > 100 MB)                   |
| ~07:08:20  | Second run reaches chunk 200; `/leak` returns **503** from here on    |
| 07:08:36   | `HighErrorRate` → **pending** (the 503s push 5xx ratio above 10 %)    |
| 07:09:13   | pprof heap profile: `main.leakHandler` holds 99.61 % of in-use heap   |
| ~07:10:00  | `POST /reset` — 200 chunks cleared, heap 2.8 MB                       |
| 07:10:37   | `HighErrorRate` → **firing** (2 min after pending — even though the cause was already removed) |
| 07:13:17   | `HighErrorRate` resolved — ~3 min after the cause was removed         |

`MemoryLeakSuspected` never fired in this run: `/reset` was called after 3 min 40 s
of pending, before its 5 min `for` window elapsed.

## Timeline (Run 3: `simulate-leak.sh` defaults — 110 requests, 1 s apart)

| Time (UTC) | Event                                                            |
|------------|------------------------------------------------------------------|
| 07:30:48   | `simulate-leak.sh` started with the new defaults                 |
| 07:32:51   | `MemoryLeakSuspected` → **pending** (request 98, heap 100.5 MB)  |
| 07:37:51   | `MemoryLeakSuspected` → **firing** (exactly 5 min after pending) |
| 07:38:09   | `POST /reset` — 110 chunks cleared, heap 1.2 MB                  |
| 07:38:22   | `MemoryLeakSuspected` resolved — 13 s after reset                |

The gauge-based leak alert cleared 13 s after recovery, while the rate-based
`HighErrorRate` (Run 1) took ~3 min because of its 5 min `rate()` window.

## Root Cause

The `/leak` endpoint allocates a 1 MB byte slice on each request and appends it
to a package-level slice (`leakyStore`). The slice stays reachable, so the
garbage collector can never free it. pprof attributes 99.61 % of in-use heap
to `main.leakHandler`; the HTTP server frames above it have 0 flat usage.

## Resolution

Immediate: `POST /reset` sets `leakyStore` to nil and calls `runtime.GC()`.

Long-term: The leak endpoint exists intentionally for demos. In a real service
the fix would be removing the unbounded cache or adding an eviction policy.

## Findings From the Re-test

1. **Unreachable trigger.** The documented reproduction (50 requests) could not
   cross the 100 MB threshold. The script default is now 110 requests.
2. **One cause, two alerts.** Once the safety cap returned 503s, `HighErrorRate`
   fired alongside `MemoryLeakSuspected`. Alerts should be correlated before
   acting on them separately.
3. **Alert wait is longer than time-to-OOM.** At 1 MB/s, heap crosses 100 MB at
   ~98 s and the alert fires at ~6 min 40 s. Without the safety cap, the 256 MiB
   container limit would be reached at ~4 min 15 s, so the container would be
   OOM-killed and restarted before the alert ever fired.
4. **Rule with no data.** `ContainerMemoryHigh` filters on `name="go-api"`, but
   cAdvisor on Docker Desktop (macOS) only exposes cgroup roots (`id="/"`,
   `/docker`, …). The query returns 0 series, so this alert cannot fire here.
5. **Lagging recovery.** `HighErrorRate` uses a 5 min `rate()` window and stayed
   firing for ~3 min after the 503s stopped.
6. **Heap recovered, process memory did not.** After `/reset`, heap dropped to
   2.8 MB but Go `Sys` stayed at 218.1 MB, because the runtime keeps freed memory
   for reuse. Recovery has to be checked at both application and container level.

## Prevention Checklist

- [x] Alert rule for heap growth (`MemoryLeakSuspected`)
- [x] Container memory limit set (`mem_limit: 256m`)
- [x] Pprof endpoints exposed for profiling
- [x] Safety cap on leak endpoint (200 chunks)
- [x] Reproduction script default exceeds the alert threshold
- [ ] Detect growth rate instead of a fixed threshold (e.g. `predict_linear`)
- [ ] `ContainerMemoryHigh` based on a metric available on every host
- [ ] Exclude intentional 503s from the safety cap from `HighErrorRate`, or document the correlation
- [ ] Automated canary that detects monotonic memory growth in CI
