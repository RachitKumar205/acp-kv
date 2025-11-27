#!/bin/bash
# high-load.sh - Scenario 4: High-load stress test (120s)
# Goal: Test ACP under high throughput
# Conditions: High concurrency (50 clients) targeting 5000 ops/sec

set -e

DURATION=${1:-120}
WORKLOAD=${2:-workloada}
OUTPUT_DIR=${3:-results/high-load}

echo "=== Scenario 4: High-Load Stress ===" | tee "$OUTPUT_DIR/scenario.log"
echo "duration: ${DURATION}s" | tee -a "$OUTPUT_DIR/scenario.log"
echo "workload: $WORKLOAD" | tee -a "$OUTPUT_DIR/scenario.log"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# Check if acp-bench exists
if [ ! -x "$SCRIPT_DIR/../../../bin/acp-bench" ]; then
    echo "[high-load] ERROR: acp-bench not found" | tee -a "$OUTPUT_DIR/scenario.log"
    exit 1
fi

# Phase 1: Start with normal latency (40s)
echo "[phase 1/3] normal operation with 1ms latency (40s)..." | tee -a "$OUTPUT_DIR/scenario.log"
for pod in acp-node-0 acp-node-1 acp-node-2; do
    "$SCRIPT_DIR/../../network/inject-latency.sh" "$pod" 1 0 | tee -a "$OUTPUT_DIR/scenario.log"
done

sleep 2

# Start port-forward
echo "[high-load] starting port-forward..." | tee -a "$OUTPUT_DIR/scenario.log"
kubectl port-forward acp-node-0 8080:8080 > /dev/null 2>&1 &
PF_PID=$!
sleep 3

# Run high-load benchmark in background
echo "[high-load] starting high-throughput stress test..." | tee -a "$OUTPUT_DIR/scenario.log"

"$SCRIPT_DIR/../../../bin/acp-bench" \
    --endpoints=localhost:8080 \
    --mode=run \
    --duration="${DURATION}s" \
    --workload=mixed \
    --concurrency=50 \
    --target-throughput=5000 \
    --output="$OUTPUT_DIR/bench-results.csv" \
    2>&1 | tee "$OUTPUT_DIR/benchmark.log" &
BENCH_PID=$!

# Wait for phase 1
sleep 40

# Phase 2: Kill a node under high load to trigger CCS drop (40s)
echo "[phase 2/3] killing node-2 under high load to stress CCS (40s)..." | tee -a "$OUTPUT_DIR/scenario.log"
kubectl delete pod acp-node-2 --wait=false | tee -a "$OUTPUT_DIR/scenario.log"

echo "[phase 2/3] waiting for CCS to drop due to high load + node loss..." | tee -a "$OUTPUT_DIR/scenario.log"
sleep 10

echo "[phase 2/3] continuing with high load on 2 nodes (30s remaining)..." | tee -a "$OUTPUT_DIR/scenario.log"
sleep 30

# Phase 3: Wait for node recovery (40s)
echo "[phase 3/3] waiting for node-2 to restart (40s)..." | tee -a "$OUTPUT_DIR/scenario.log"
kubectl wait --for=condition=ready pod/acp-node-2 --timeout=40s | tee -a "$OUTPUT_DIR/scenario.log" || true
echo "[phase 3/3] node-2 back, CCS should recover under continued high load..." | tee -a "$OUTPUT_DIR/scenario.log"

# Wait for benchmark to complete
wait $BENCH_PID

# Cleanup port-forward
echo "[high-load] cleaning up port-forward..." | tee -a "$OUTPUT_DIR/scenario.log"
kill $PF_PID 2>/dev/null || true
sleep 1

echo "[high-load] stress test complete!" | tee -a "$OUTPUT_DIR/scenario.log"

# expected results:
# - Throughput saturation point
# - Latency under high load
# - Quorum adjustments if load causes degradation
