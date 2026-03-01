#!/usr/bin/env bash
set -euo pipefail

# simulate-leak.sh — Generate a gradual memory leak for observability testing.
# Usage: ./scripts/simulate-leak.sh [REQUESTS] [DELAY_SECONDS]

API_URL="${API_URL:-http://localhost:8080}"
REQUESTS="${1:-50}"
DELAY="${2:-2}"

echo "=== Memory Leak Simulation ==="
echo "Target:   ${API_URL}"
echo "Requests: ${REQUESTS}"
echo "Delay:    ${DELAY}s between requests"
echo ""

# Pre-flight health check
echo "Checking API health..."
if ! curl -sf "${API_URL}/health" > /dev/null 2>&1; then
    echo "ERROR: API not reachable at ${API_URL}/health"
    echo "Start the stack with: docker compose up -d"
    exit 1
fi
echo "API is healthy. Starting leak simulation..."
echo ""

for i in $(seq 1 "$REQUESTS"); do
    response=$(curl -sf "${API_URL}/leak" 2>&1) || {
        echo "[${i}/${REQUESTS}] WARN: request failed, continuing..."
        sleep "$DELAY"
        continue
    }
    echo "[${i}/${REQUESTS}] ${response}"
    sleep "$DELAY"
done

echo ""
echo "=== Simulation Complete ==="
echo "Check Grafana:    http://localhost:3000"
echo "Check Prometheus: http://localhost:9090"
echo "Reset memory:     curl -X POST ${API_URL}/reset"
