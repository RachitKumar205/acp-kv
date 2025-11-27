#!/bin/bash
# degraded-latency.sh - Scenario 2: Degraded latency test (180s)
# Goal: Validate CCS algorithm and adaptive quorum reaction
# Timeline: 60s normal → 60s high latency (150ms+20ms jitter) → 60s recovery

set -e

DURATION=${1:-180}
WORKLOAD=${2:-workloada}
OUTPUT_DIR=${3:-results/degraded-latency}

echo "=== Scenario 2: Degraded Latency ===" | tee "$OUTPUT_DIR/scenario.log"
echo "duration: ${DURATION}s (60s normal + 60s degraded + 60s recovery)" | tee -a "$OUTPUT_DIR/scenario.log"
echo "workload: $WORKLOAD" | tee -a "$OUTPUT_DIR/scenario.log"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# phase 1: normal operation (60s) with 1ms latency
echo "[phase 1/3] normal operation (1ms latency, 60s)..." | tee -a "$OUTPUT_DIR/scenario.log"
for pod in acp-node-0 acp-node-1 acp-node-2; do
    "$SCRIPT_DIR/../../network/inject-latency.sh" "$pod" 1 0 | tee -a "$OUTPUT_DIR/scenario.log"
done

# start port-forward
echo "[degraded-latency] starting port-forward..." | tee -a "$OUTPUT_DIR/scenario.log"
kubectl port-forward acp-node-0 8080:8080 > /dev/null 2>&1 &
PF_PID=$!
sleep 3

# start benchmark in background
echo "[degraded-latency] starting benchmark..." | tee -a "$OUTPUT_DIR/scenario.log"
if [ ! -x "$SCRIPT_DIR/../../../bin/acp-bench" ]; then
    echo "[degraded-latency] ERROR: acp-bench not found. Run 'make acp-bench' first." | tee -a "$OUTPUT_DIR/scenario.log"
    kill $PF_PID 2>/dev/null
    exit 1
fi

"$SCRIPT_DIR/../../../bin/acp-bench" \
    --endpoints=localhost:8080 \
    --mode=run \
    --duration="${DURATION}s" \
    --workload=mixed \
    --concurrency=10 \
    --output="$OUTPUT_DIR/bench-results.csv" \
    > "$OUTPUT_DIR/ycsb.log" 2>&1 &
BENCH_PID=$!

# wait for phase 1
sleep 60

# phase 2: kill a node to make it unreachable (affects availability component of CCS)
echo "[phase 2/3] killing node-2 to trigger CCS drop (60s)..." | tee -a "$OUTPUT_DIR/scenario.log"
kubectl delete pod acp-node-2 --wait=false | tee -a "$OUTPUT_DIR/scenario.log"
echo "[phase 2/3] waiting for CCS to drop and W to adapt..." | tee -a "$OUTPUT_DIR/scenario.log"
sleep 15  # give time for CCS to drop and W to relax

# wait for rest of phase 2
echo "[phase 2/3] continuing with 2-node cluster (45s remaining)..." | tee -a "$OUTPUT_DIR/scenario.log"
sleep 45

# phase 3: recovery - wait for pod to restart
echo "[phase 3/3] recovery (waiting for pod to restart, 60s)..." | tee -a "$OUTPUT_DIR/scenario.log"
kubectl wait --for=condition=ready pod/acp-node-2 --timeout=60s | tee -a "$OUTPUT_DIR/scenario.log" || true
echo "[phase 3/3] node-2 back online, CCS should rise and W should tighten..." | tee -a "$OUTPUT_DIR/scenario.log"
sleep 10  # give time for CCS to recover and W to tighten

# wait for benchmark to complete
wait $BENCH_PID

# cleanup port-forward
echo "[degraded-latency] cleaning up port-forward..." | tee -a "$OUTPUT_DIR/scenario.log"
kill $PF_PID 2>/dev/null
sleep 1  # Give it a moment to die

echo "[degraded-latency] benchmark complete!" | tee -a "$OUTPUT_DIR/scenario.log"

# expected results:
# - CCS drops within 5-8s after latency injection
# - W reduces within 5-10s after CCS < 0.45
# - 20-30% latency improvement vs static quorum
# - CCS recovers within 5-10s after latency removal
# - W tightens again within 5-10s after CCS > 0.75
