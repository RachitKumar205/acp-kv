#!/bin/bash
# baseline.sh - Scenario 1: Baseline performance test (120s)
# Goal: Measure ACP performance when cluster is perfectly healthy
# Conditions: 1ms latency, no jitter, no partitions, no packet loss

set -e

DURATION=${1:-120}
WORKLOAD=${2:-workloada}
OUTPUT_DIR=${3:-results/baseline}

echo "=== Scenario 1: Baseline ===" | tee "$OUTPUT_DIR/scenario.log"
echo "duration: ${DURATION}s" | tee -a "$OUTPUT_DIR/scenario.log"
echo "workload: $WORKLOAD" | tee -a "$OUTPUT_DIR/scenario.log"

# inject minimal realistic latency (1ms)
echo "[baseline] injecting 1ms baseline latency..." | tee -a "$OUTPUT_DIR/scenario.log"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
for pod in acp-node-0 acp-node-1 acp-node-2; do
    "$SCRIPT_DIR/../../network/inject-latency.sh" "$pod" 1 0 | tee -a "$OUTPUT_DIR/scenario.log"
done

# give network time to stabilize
sleep 2

# run benchmark using custom acp-bench tool
echo "[baseline] running benchmark..." | tee -a "$OUTPUT_DIR/scenario.log"

if [ ! -x "$SCRIPT_DIR/../../../bin/acp-bench" ]; then
    echo "[baseline] ERROR: acp-bench not found. Run 'make acp-bench' first." | tee -a "$OUTPUT_DIR/scenario.log"
    exit 1
fi

# start port-forward in background
echo "[baseline] starting port-forward..." | tee -a "$OUTPUT_DIR/scenario.log"
kubectl port-forward acp-node-0 8080:8080 > /dev/null 2>&1 &
PF_PID=$!
sleep 3

# run benchmark
"$SCRIPT_DIR/../../../bin/acp-bench" \
    --endpoints=localhost:8080 \
    --mode=run \
    --duration="${DURATION}s" \
    --workload=mixed \
    --concurrency=10 \
    --output="$OUTPUT_DIR/bench-results.csv" \
    2>&1 | tee "$OUTPUT_DIR/ycsb.log"

# cleanup port-forward
echo "[baseline] cleaning up port-forward..." | tee -a "$OUTPUT_DIR/scenario.log"
kill $PF_PID 2>/dev/null || true
sleep 1  # Give it a moment to die

echo "[baseline] benchmark complete!" | tee -a "$OUTPUT_DIR/scenario.log"

# expected results:
# - ACP Strong slightly faster than etcd
# - Redis fastest but no consistency
# - CCS remains >0.75 throughout
# - W remains at max safe value
# - Zero staleness violations
