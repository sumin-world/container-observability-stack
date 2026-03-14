# Case Study: Multi-Source Data Aggregation Failure

## Pattern

When a system aggregates pricing data from multiple external sources,
**unit mismatch** between sources can produce catastrophically wrong values.

## Example Scenario

```
Source A reports: 934   (unit: per 100 units of foreign currency)
Source B reports: 9.34  (unit: per 1 unit of foreign currency)

Naive average: (934 + 9.34) / 2 = 471.67
Actual value:  934

Error: ~50% below true price
```

The resulting value (471) is not obviously "wrong" — it falls within a plausible
range, which is why simple min/max bounds checking may not catch it.

## Why This Is Hard to Detect

1. **Each source value is valid in isolation.** 934 and 9.34 are both correct
   numbers — just in different units.
2. **The error only appears when combining sources.** Any single-source system
   would not have this bug.
3. **The aggregated value looks plausible.** A 50% drop is large, but not
   impossible in volatile markets. Without context, automated systems may
   accept it.

## Defense Layers

### Layer 1: Input Normalization

All data sources must be converted to a canonical unit before aggregation.
This is a **data pipeline design** issue, not a runtime validation issue.

```
source_a_rate = fetch_source_a()           # returns per-100-unit
source_b_rate = fetch_source_b() * 100     # normalize to per-100-unit
aggregated    = (source_a_rate + source_b_rate) / 2
```

### Layer 2: Cross-Source Divergence Check

If Source A and Source B differ by more than a threshold (e.g., 10%),
**reject the update entirely** rather than averaging incorrect values.

```
divergence = abs(a - b) / max(a, b) * 100
if divergence > 10%:
    reject and alert
```

### Layer 3: Rate-of-Change Validation

If the computed rate differs from the previous known rate by more than a
threshold (e.g., 30%), hold the update for review.

```
change = abs(new_rate - previous_rate) / previous_rate * 100
if change > 30%:
    reject and alert
```

### Layer 4: Circuit Breaker

If multiple consecutive anomalies are detected, stop serving rates entirely
until manual review confirms the data pipeline is healthy.

States: CLOSED (normal) → OPEN (blocked) → HALF-OPEN (probe)

### Layer 5: Observability

The key to reducing **detection time** from minutes to seconds:

```promql
# Source divergence (should be near 0%)
exchange_rate_source_divergence_percent

# Rate of change (should be < 10% normally)
exchange_rate_change_percent

# Transaction volume spike (indicates users exploiting anomaly)
rate(exchange_executed_total[1m])
  > 10 * avg_over_time(rate(exchange_executed_total[1m])[1h:])
```

**Target: detect and block within 30 seconds, not 7 minutes.**

## Concurrency Considerations

In high-throughput systems, rate data is read and written concurrently.
This introduces Time-of-Check to Time-of-Use (TOCTOU) bugs:

```
Thread A: reads rate = 934 (valid)
Thread B: updates rate to 471 (anomalous)
Thread A: uses rate 934 for validation check → passes
Thread A: executes exchange → but actual rate is now 471
```

**Mitigation:**
- Read rate and execute transaction under the same lock
- Double-check pattern: re-verify rate after acquiring lock
- Go: `sync.RWMutex` for concurrent reads, exclusive writes
- Go: `go test -race` to detect race conditions in CI

## Related Reading

- [Circuit Breaker Pattern — Martin Fowler](https://martinfowler.com/bliki/CircuitBreaker.html)
- [Google SRE Book — Monitoring Distributed Systems](https://sre.google/sre-book/monitoring-distributed-systems/)
- [Resilience4j Documentation](https://resilience4j.readme.io/)

## How This Connects to Our Stack

This service is part of the
[container-observability-stack](https://github.com/sumin-world/container-observability-stack),
demonstrating how Prometheus metrics, Grafana dashboards, and alert rules
can detect data pipeline failures in real time.

| Component | Role |
|-----------|------|
| `exchange-rate-service` | Simulates vulnerable and hardened rate endpoints |
| Prometheus alerts | `ExchangeRateAnomalyDetected`, `SourceDivergenceHigh`, `CircuitBreakerOpen` |
| Grafana dashboard | "Exchange Rate Service — Resilience" |
| Simulation script | `scripts/simulate-exchange-anomaly.sh` |
