#!/bin/bash
# high-load.sh - YCSB high-load scenario
# Usage: ./high-load.sh <system> <duration> <workload> <output_dir>
#
# Tests throughput ceiling with gradually increasing load
# Phase 1 (0-30s): Low load (100 ops/sec)
# Phase 2 (30-60s): Medium load (1000 ops/sec)
# Phase 3 (60-120s): High load (10000 ops/sec)

set -e

SYSTEM=${1:-acp}
DURATION=${2:-120}
WORKLOAD=${3:-workloada}
OUTPUT_DIR=${4:-results/high-load}

echo "=== YCSB High-Load Benchmark ===" | tee "$OUTPUT_DIR/scenario.log"
echo "system: $SYSTEM" | tee -a "$OUTPUT_DIR/scenario.log"
echo "duration: ${DURATION}s" | tee -a "$OUTPUT_DIR/scenario.log"
echo "workload: $WORKLOAD" | tee -a "$OUTPUT_DIR/scenario.log"

# Get script directory
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
YCSB_BIN="$SCRIPT_DIR/../../../bin/go-ycsb"

if [ ! -x "$YCSB_BIN" ]; then
    echo "ERROR: go-ycsb binary not found at $YCSB_BIN" | tee -a "$OUTPUT_DIR/scenario.log"
    exit 1
fi

# Function to run YCSB (either locally or in-cluster)
run_ycsb() {
    if [ "$USE_IN_CLUSTER" = "true" ]; then
        # Run inside cluster via kubectl exec
        kubectl exec ycsb-runner -- go-ycsb "$@"
    else
        # Run locally
        $YCSB_BIN "$@"
    fi
}


# Configure endpoints based on system
case $SYSTEM in
    acp)
        DB_NAME="acp"
        ENDPOINTS="localhost:8080,localhost:8081,localhost:8082"
        ENDPOINT_PARAM="acp.endpoints"
        ;;
    redis)
        DB_NAME="redis"
        ENDPOINTS="redis-bench-0.redis-headless:6379"
        ENDPOINT_PARAM="redis.addr"
        USE_IN_CLUSTER=true
        ;;
    etcd)
        DB_NAME="etcd"
        ENDPOINTS="localhost:2379"
        ENDPOINT_PARAM="etcd.endpoints"
        ;;
    *)
        echo "ERROR: Unknown system: $SYSTEM" | tee -a "$OUTPUT_DIR/scenario.log"
        exit 1
        ;;
esac

# Setup port forwarding (skip for in-cluster Redis)
if [ "$SYSTEM" = "acp" ]; then
    echo "[high-load] Setting up port forwarding..." | tee -a "$OUTPUT_DIR/scenario.log"
    kubectl port-forward acp-node-0 8080:8080 > /dev/null 2>&1 &
    PF_PID1=$!
    kubectl port-forward acp-node-1 8081:8080 > /dev/null 2>&1 &
    PF_PID2=$!
    kubectl port-forward acp-node-2 8082:8080 > /dev/null 2>&1 &
    PF_PID3=$!
    sleep 3
elif [ "$SYSTEM" = "etcd" ]; then
    echo "[SCENARIO] Setting up port forwarding..." | tee -a "$OUTPUT_DIR/scenario.log"
    kubectl port-forward etcd-bench-0 2379:2379 > /dev/null 2>&1 &
    PF_PID1=$!
    sleep 3
fi


# Set workload path based on execution mode
if [ "$USE_IN_CLUSTER" = "true" ]; then
    WORKLOAD_PATH="/workloads/$WORKLOAD.properties"
else
    WORKLOAD_PATH="$SCRIPT_DIR/../../workloads/$WORKLOAD.properties"
fi

# Load data phase
echo "[high-load] Loading data..." | tee -a "$OUTPUT_DIR/scenario.log"
if [ "$SYSTEM" = "redis" ]; then
    run_ycsb load $DB_NAME \
        -P "$WORKLOAD_PATH" \
        -p $ENDPOINT_PARAM="$ENDPOINTS" \
        -p redis.mode=cluster \
        -p redis.datatype=string \
        -p recordcount=10000 \
        -p threadcount=10 \
        > "$OUTPUT_DIR/load.log" 2>&1
else
    run_ycsb load $DB_NAME \
        -P "$WORKLOAD_PATH" \
        -p $ENDPOINT_PARAM="$ENDPOINTS" \
        -p recordcount=10000 \
        -p threadcount=10 \
        > "$OUTPUT_DIR/load.log" 2>&1
fi

echo "[high-load] Data loaded successfully" | tee -a "$OUTPUT_DIR/scenario.log"

# Calculate phase durations
PHASE1_DURATION=30  # Low load
PHASE2_DURATION=30  # Medium load
PHASE3_DURATION=$((DURATION - PHASE1_DURATION - PHASE2_DURATION))  # High load

if [ $PHASE3_DURATION -lt 0 ]; then
    PHASE3_DURATION=40
    TOTAL_DURATION=$((PHASE1_DURATION + PHASE2_DURATION + PHASE3_DURATION))
    echo "[high-load] Adjusted total duration to ${TOTAL_DURATION}s for 3 phases" | tee -a "$OUTPUT_DIR/scenario.log"
    DURATION=$TOTAL_DURATION
fi

# Record start timestamp for Prometheus metrics collection
METRICS_START=$(date -u +%s)

# Phase 1: Low load (100 ops/sec)
echo "[high-load] Phase 1: Low load - 100 ops/sec (${PHASE1_DURATION}s)..." | tee -a "$OUTPUT_DIR/scenario.log"
PHASE1_OPS=$((PHASE1_DURATION * 100))

if [ "$SYSTEM" = "redis" ]; then
    run_ycsb run $DB_NAME \
        -P "$WORKLOAD_PATH" \
        -p $ENDPOINT_PARAM="$ENDPOINTS" \
        -p redis.mode=cluster \
        -p redis.datatype=string \
        -p operationcount=$PHASE1_OPS \
        -p threadcount=2 \
        -p target=100 \
        > "$OUTPUT_DIR/phase1.log" 2>&1
else
    run_ycsb run $DB_NAME \
        -P "$WORKLOAD_PATH" \
        -p $ENDPOINT_PARAM="$ENDPOINTS" \
        -p operationcount=$PHASE1_OPS \
        -p threadcount=2 \
        -p target=100 \
        > "$OUTPUT_DIR/phase1.log" 2>&1
fi

echo "[high-load] Phase 1 complete" | tee -a "$OUTPUT_DIR/scenario.log"

# Phase 2: Medium load (1000 ops/sec)
echo "[high-load] Phase 2: Medium load - 1000 ops/sec (${PHASE2_DURATION}s)..." | tee -a "$OUTPUT_DIR/scenario.log"
PHASE2_OPS=$((PHASE2_DURATION * 1000))

if [ "$SYSTEM" = "redis" ]; then
    run_ycsb run $DB_NAME \
        -P "$WORKLOAD_PATH" \
        -p $ENDPOINT_PARAM="$ENDPOINTS" \
        -p redis.mode=cluster \
        -p redis.datatype=string \
        -p operationcount=$PHASE2_OPS \
        -p threadcount=10 \
        -p target=1000 \
        > "$OUTPUT_DIR/phase2.log" 2>&1
else
    run_ycsb run $DB_NAME \
        -P "$WORKLOAD_PATH" \
        -p $ENDPOINT_PARAM="$ENDPOINTS" \
        -p operationcount=$PHASE2_OPS \
        -p threadcount=10 \
        -p target=1000 \
        > "$OUTPUT_DIR/phase2.log" 2>&1
fi

echo "[high-load] Phase 2 complete" | tee -a "$OUTPUT_DIR/scenario.log"

# Phase 3: High load (10000 ops/sec)
echo "[high-load] Phase 3: High load - 10000 ops/sec (${PHASE3_DURATION}s)..." | tee -a "$OUTPUT_DIR/scenario.log"
PHASE3_OPS=$((PHASE3_DURATION * 10000))

if [ "$SYSTEM" = "redis" ]; then
    run_ycsb run $DB_NAME \
        -P "$WORKLOAD_PATH" \
        -p $ENDPOINT_PARAM="$ENDPOINTS" \
        -p redis.mode=cluster \
        -p redis.datatype=string \
        -p operationcount=$PHASE3_OPS \
        -p threadcount=50 \
        -p target=10000 \
        > "$OUTPUT_DIR/phase3.log" 2>&1
else
    run_ycsb run $DB_NAME \
        -P "$WORKLOAD_PATH" \
        -p $ENDPOINT_PARAM="$ENDPOINTS" \
        -p operationcount=$PHASE3_OPS \
        -p threadcount=50 \
        -p target=10000 \
        > "$OUTPUT_DIR/phase3.log" 2>&1
fi

echo "[high-load] Phase 3 complete" | tee -a "$OUTPUT_DIR/scenario.log"

# Record end timestamp
METRICS_END=$(date -u +%s)
ACTUAL_DURATION=$((METRICS_END - METRICS_START))

echo "[high-load] Benchmark completed in ${ACTUAL_DURATION}s" | tee -a "$OUTPUT_DIR/scenario.log"

# Extract metrics from all phases
echo "[high-load] Extracting metrics..." | tee -a "$OUTPUT_DIR/scenario.log"

{
    echo "=== PHASE 1: Low Load (100 ops/sec) ==="
    grep -E "\[OVERALL\]|\[READ\]|\[UPDATE\]" "$OUTPUT_DIR/phase1.log" || true
    echo ""
    echo "=== PHASE 2: Medium Load (1000 ops/sec) ==="
    grep -E "\[OVERALL\]|\[READ\]|\[UPDATE\]" "$OUTPUT_DIR/phase2.log" || true
    echo ""
    echo "=== PHASE 3: High Load (10000 ops/sec) ==="
    grep -E "\[OVERALL\]|\[READ\]|\[UPDATE\]" "$OUTPUT_DIR/phase3.log" || true
} > "$OUTPUT_DIR/metrics.txt"

# Analyze drop-off point (when latency > 100ms OR error rate > 5%)
echo "" | tee -a "$OUTPUT_DIR/metrics.txt"
echo "=== DROP-OFF POINT ANALYSIS ===" | tee -a "$OUTPUT_DIR/metrics.txt"

# Extract average latencies from each phase (in microseconds)
PHASE1_LATENCY=$(grep "\[READ\]" "$OUTPUT_DIR/phase1.log" | grep "AverageLatency" | awk '{print $3}' || echo "0")
PHASE2_LATENCY=$(grep "\[READ\]" "$OUTPUT_DIR/phase2.log" | grep "AverageLatency" | awk '{print $3}' || echo "0")
PHASE3_LATENCY=$(grep "\[READ\]" "$OUTPUT_DIR/phase3.log" | grep "AverageLatency" | awk '{print $3}' || echo "0")

# Convert to milliseconds (divide by 1000)
PHASE1_MS=$(echo "scale=2; $PHASE1_LATENCY / 1000" | bc 2>/dev/null || echo "0")
PHASE2_MS=$(echo "scale=2; $PHASE2_LATENCY / 1000" | bc 2>/dev/null || echo "0")
PHASE3_MS=$(echo "scale=2; $PHASE3_LATENCY / 1000" | bc 2>/dev/null || echo "0")

echo "Phase 1 Avg Latency: ${PHASE1_MS}ms" | tee -a "$OUTPUT_DIR/metrics.txt"
echo "Phase 2 Avg Latency: ${PHASE2_MS}ms" | tee -a "$OUTPUT_DIR/metrics.txt"
echo "Phase 3 Avg Latency: ${PHASE3_MS}ms" | tee -a "$OUTPUT_DIR/metrics.txt"

# Check error rates
PHASE1_ERRORS=$(grep -i "error\|failed" "$OUTPUT_DIR/phase1.log" | wc -l || echo "0")
PHASE2_ERRORS=$(grep -i "error\|failed" "$OUTPUT_DIR/phase2.log" | wc -l || echo "0")
PHASE3_ERRORS=$(grep -i "error\|failed" "$OUTPUT_DIR/phase3.log" | wc -l || echo "0")

echo "Phase 1 Errors: $PHASE1_ERRORS" | tee -a "$OUTPUT_DIR/metrics.txt"
echo "Phase 2 Errors: $PHASE2_ERRORS" | tee -a "$OUTPUT_DIR/metrics.txt"
echo "Phase 3 Errors: $PHASE3_ERRORS" | tee -a "$OUTPUT_DIR/metrics.txt"

# Determine max sustainable throughput (phase before drop-off)
DROP_OFF="None detected"
MAX_THROUGHPUT="10000+ ops/sec"

if (( $(echo "$PHASE1_MS > 100" | bc -l 2>/dev/null || echo 0) )) || [ "$PHASE1_ERRORS" -gt 100 ]; then
    DROP_OFF="Phase 1 (100 ops/sec)"
    MAX_THROUGHPUT="<100 ops/sec"
elif (( $(echo "$PHASE2_MS > 100" | bc -l 2>/dev/null || echo 0) )) || [ "$PHASE2_ERRORS" -gt 500 ]; then
    DROP_OFF="Phase 2 (1000 ops/sec)"
    MAX_THROUGHPUT="~100-1000 ops/sec"
elif (( $(echo "$PHASE3_MS > 100" | bc -l 2>/dev/null || echo 0) )) || [ "$PHASE3_ERRORS" -gt 5000 ]; then
    DROP_OFF="Phase 3 (10000 ops/sec)"
    MAX_THROUGHPUT="~1000-10000 ops/sec"
fi

echo "" | tee -a "$OUTPUT_DIR/metrics.txt"
echo "Drop-off Point: $DROP_OFF" | tee -a "$OUTPUT_DIR/metrics.txt"
echo "Max Sustainable Throughput: $MAX_THROUGHPUT" | tee -a "$OUTPUT_DIR/metrics.txt"
echo "" | tee -a "$OUTPUT_DIR/metrics.txt"

# Collect Prometheus metrics for ACP
if [ "$SYSTEM" = "acp" ]; then
    echo "[high-load] Collecting Prometheus metrics..." | tee -a "$OUTPUT_DIR/scenario.log"

    # Set up port-forward to Prometheus if not already running
    PROM_CHECK=$(curl -s 'http://localhost:9090/api/v1/query?query=up' 2>&1)
    if [[ "$PROM_CHECK" != *'"status":"success"'* ]]; then
        echo "[high-load] Setting up Prometheus port-forward..." | tee -a "$OUTPUT_DIR/scenario.log"
        kubectl port-forward -n default svc/prometheus 9090:9090 > /dev/null 2>&1 &
        PROM_PF_PID=$!
        sleep 5
    fi

    "$SCRIPT_DIR/../collect-metrics.sh" "$METRICS_START" "$METRICS_END" "$OUTPUT_DIR/prometheus-metrics.csv" 2>&1 | tee -a "$OUTPUT_DIR/scenario.log" || echo "Warning: Prometheus collection failed" | tee -a "$OUTPUT_DIR/scenario.log"

    # Cleanup Prometheus port-forward if we started it
    if [ -n "$PROM_PF_PID" ]; then
        kill $PROM_PF_PID 2>/dev/null || true
    fi
fi

# Cleanup port forwarding
if [ "$SYSTEM" = "acp" ]; then
    if [ -n "$PF_PID1" ]; then kill $PF_PID1 2>/dev/null || true; fi
    if [ -n "$PF_PID2" ]; then kill $PF_PID2 2>/dev/null || true; fi
    if [ -n "$PF_PID3" ]; then kill $PF_PID3 2>/dev/null || true; fi
elif [ "$SYSTEM" = "redis" ] || [ "$SYSTEM" = "etcd" ]; then
    if [ -n "$PF_PID1" ]; then kill $PF_PID1 2>/dev/null || true; fi
fi

echo "[high-load] Benchmark complete! Results in $OUTPUT_DIR" | tee -a "$OUTPUT_DIR/scenario.log"
