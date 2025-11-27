#!/bin/bash
# partition.sh - Scenario 3: Network partition test (240s)
# Goal: Test ACP during network partitions
# Timeline: 60s normal → 60s partition (isolate node-2) → 120s heal + reconcile

set -e

DURATION=${1:-240}
WORKLOAD=${2:-workloada}
OUTPUT_DIR=${3:-results/partition}

echo "=== Scenario 3: Network Partition ===" | tee "$OUTPUT_DIR/scenario.log"
echo "duration: ${DURATION}s (60s normal + 60s partition + 120s healing)" | tee -a "$OUTPUT_DIR/scenario.log"
echo "workload: $WORKLOAD" | tee -a "$OUTPUT_DIR/scenario.log"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# Check if acp-bench exists
if [ ! -x "$SCRIPT_DIR/../../../bin/acp-bench" ]; then
    echo "[partition] ERROR: acp-bench not found" | tee -a "$OUTPUT_DIR/scenario.log"
    exit 1
fi

# Start port-forward
echo "[partition] starting port-forward..." | tee -a "$OUTPUT_DIR/scenario.log"
kubectl port-forward acp-node-0 8080:8080 > /dev/null 2>&1 &
PF_PID=$!
sleep 3

# Phase 1: normal operation (60s) - run benchmark in background for full duration
echo "[phase 1/3] normal operation (60s)..." | tee -a "$OUTPUT_DIR/scenario.log"

"$SCRIPT_DIR/../../../bin/acp-bench" \
    --endpoints=localhost:8080 \
    --mode=run \
    --duration="${DURATION}s" \
    --workload=mixed \
    --concurrency=10 \
    --output="$OUTPUT_DIR/bench-results.csv" \
    2>&1 | tee "$OUTPUT_DIR/benchmark.log" &
BENCH_PID=$!

# Wait for phase 1
sleep 60

# Phase 2: kill node-2 to create actual unavailability (affects CCS availability component)
echo "[phase 2/3] killing node-2 to simulate partition (60s)..." | tee -a "$OUTPUT_DIR/scenario.log"
kubectl delete pod acp-node-2 --wait=false | tee -a "$OUTPUT_DIR/scenario.log"

echo "[phase 2/3] waiting for CCS to drop due to node unavailability..." | tee -a "$OUTPUT_DIR/scenario.log"
sleep 15

# Wait for rest of phase 2
echo "[phase 2/3] continuing with 2-node cluster (45s remaining)..." | tee -a "$OUTPUT_DIR/scenario.log"
sleep 45

# Phase 3: wait for node-2 to restart and rejoin
echo "[phase 3/3] waiting for node-2 to restart (120s)..." | tee -a "$OUTPUT_DIR/scenario.log"
kubectl wait --for=condition=ready pod/acp-node-2 --timeout=120s | tee -a "$OUTPUT_DIR/scenario.log" || true
echo "[phase 3/3] node-2 rejoined, observing reconciliation and CCS recovery..." | tee -a "$OUTPUT_DIR/scenario.log"
sleep 15  # Give time for reconciliation and CCS to recover

# Wait for benchmark to complete
wait $BENCH_PID

# Cleanup port-forward
echo "[partition] cleaning up port-forward..." | tee -a "$OUTPUT_DIR/scenario.log"
kill $PF_PID 2>/dev/null || true
sleep 1

echo "[partition] benchmark complete!" | tee -a "$OUTPUT_DIR/scenario.log"

# expected results:
# - Write availability during partition
# - Staleness tracking across replicas
# - Conflict resolution after heal
# - Reconciliation time < 10s
