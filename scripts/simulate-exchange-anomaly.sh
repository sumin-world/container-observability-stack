#!/usr/bin/env bash
set -euo pipefail

# simulate-exchange-anomaly.sh — Compare unsafe vs safe exchange rate endpoints.
# Usage: ./scripts/simulate-exchange-anomaly.sh [REQUESTS] [DELAY]

API_URL="${API_URL:-http://localhost:8082}"
REQUESTS="${1:-100}"
DELAY="${2:-0.5}"

echo "=== Exchange Rate Anomaly Simulation ==="
echo "Target:   ${API_URL}"
echo "Requests: ${REQUESTS}"
echo "Delay:    ${DELAY}s"
echo ""

# Health check
echo "Checking service health..."
if ! curl -sf "${API_URL}/health" > /dev/null 2>&1; then
    echo "ERROR: Service not reachable at ${API_URL}/health"
    echo "Start the stack with: docker compose up -d"
    exit 1
fi
echo "Service is healthy."
echo ""

echo "--- Phase 1: Rate Endpoint Comparison (unsafe vs safe) ---"
echo ""

unsafe_ok=0
unsafe_fail=0
safe_ok=0
safe_blocked=0

for i in $(seq 1 "$REQUESTS"); do
    # Unsafe endpoint — no validation
    unsafe_status=$(curl -sf -o /dev/null -w "%{http_code}" "${API_URL}/rate/unsafe/JPY" 2>&1) || unsafe_status="000"
    if [ "$unsafe_status" = "200" ]; then
        ((unsafe_ok++))
    else
        ((unsafe_fail++))
    fi

    # Safe endpoint — full validation
    safe_status=$(curl -sf -o /dev/null -w "%{http_code}" "${API_URL}/rate/safe/JPY" 2>&1) || safe_status="000"
    if [ "$safe_status" = "200" ]; then
        ((safe_ok++))
    else
        ((safe_blocked++))
    fi

    if (( i % 20 == 0 )); then
        echo "[${i}/${REQUESTS}] unsafe: ${unsafe_ok} ok / ${unsafe_fail} fail | safe: ${safe_ok} ok / ${safe_blocked} blocked"
    fi

    sleep "$DELAY"
done

echo ""
echo "=== Rate Endpoint Results ==="
echo "Unsafe: ${unsafe_ok} passed, ${unsafe_fail} failed (anomalies passed through!)"
echo "Safe:   ${safe_ok} passed, ${safe_blocked} blocked (anomalies caught)"
echo ""

echo "--- Phase 2: Concurrent Exchange (race condition demo) ---"
echo ""

# Fire 20 concurrent unsafe exchanges
echo "Firing 20 concurrent UNSAFE exchanges..."
for i in $(seq 1 20); do
    curl -sf "${API_URL}/exchange/unsafe" > /dev/null 2>&1 &
done
wait
echo "Done. Check race_condition_detected_total in Grafana."
echo ""

# Fire 20 concurrent safe exchanges
echo "Firing 20 concurrent SAFE exchanges..."
for i in $(seq 1 20); do
    curl -sf "${API_URL}/exchange/safe" > /dev/null 2>&1 &
done
wait
echo "Done."
echo ""

# Show final status
echo "=== Final Status ==="
curl -sf "${API_URL}/status" 2>&1 | python3 -m json.tool 2>/dev/null || curl -sf "${API_URL}/status"
echo ""
echo "=== Check Dashboards ==="
echo "Grafana: http://localhost:3000 → Exchange Rate Service — Resilience"
echo "Prometheus: http://localhost:9090"
echo "Reset:  curl -X POST ${API_URL}/reset"
