#!/usr/bin/env bash
set -euo pipefail

# simulate-leak.sh — Generate a gradual memory leak for observability testing.
# Usage: ./scripts/simulate-leak.sh [REQUESTS] [DELAY_SECONDS]
#
# Each /leak call retains 1 MB. Heap crosses the 100 MB MemoryLeakSuspected
# threshold at ~98 requests (measured 2026-10-11), so the default must exceed
# that. The alert fires only after the threshold holds for 5 minutes — do not
# call /reset before then. The API caps the leak at 200 chunks and answers
# HTTP 503 beyond that; those 503s also count toward HighErrorRate.

API_URL="${API_URL:-http://localhost:8080}"
REQUESTS="${1:-110}"
DELAY="${2:-1}"

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
    # Print the status code instead of hiding it behind curl -f, so a 503 from
    # the safety cap is distinguishable from the API being down.
    out=$(curl -s -w $'\n%{http_code}' "${API_URL}/leak" 2>/dev/null) || out=$'\n000'
    code="${out##*$'\n'}"
    body="${out%$'\n'*}"
    case "$code" in
        200) echo "[${i}/${REQUESTS}] ${body}" ;;
        503) echo "[${i}/${REQUESTS}] HTTP 503 (safety cap reached): ${body}" ;;
        000) echo "[${i}/${REQUESTS}] WARN: API unreachable, continuing..." ;;
        *)   echo "[${i}/${REQUESTS}] WARN: HTTP ${code}: ${body}" ;;
    esac
    sleep "$DELAY"
done

echo ""
echo "=== Simulation Complete ==="
echo "Check alerts:     http://localhost:9090/alerts (MemoryLeakSuspected fires ~5 min after heap > 100 MB)"
echo "Check Grafana:    http://localhost:3000"
echo "Reset memory:     curl -X POST ${API_URL}/reset"
