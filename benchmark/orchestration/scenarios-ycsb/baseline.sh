#!/bin/bash
# baseline.sh - YCSB baseline performance test
# Usage: ./baseline.sh <system> <duration> <workload> <output_dir>
#
# system: acp, redis, or etcd
# duration: benchmark duration in seconds (default: 120)
# workload: YCSB workload file (default: workloada)
# output_dir: directory for results (default: results/baseline)

set -e

SYSTEM=${1:-acp}
DURATION=${2:-120}
WORKLOAD=${3:-workloada}
OUTPUT_DIR=${4:-results/baseline}

echo "=== YCSB Baseline Benchmark ===" | tee "$OUTPUT_DIR/scenario.log"
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
        POD_PREFIX="acp-node"
        ;;
    redis)
        DB_NAME="redis"
        ENDPOINTS="redis-bench-0.redis-headless:6379"
        ENDPOINT_PARAM="redis.addr"
        POD_PREFIX="redis-bench"
        USE_IN_CLUSTER=true
        ;;
    etcd)
        DB_NAME="etcd"
        ENDPOINTS="localhost:2379"
        ENDPOINT_PARAM="etcd.endpoints"
        POD_PREFIX="etcd-bench"
        ;;
    *)
        echo "ERROR: Unknown system: $SYSTEM. Must be acp, redis, or etcd" | tee -a "$OUTPUT_DIR/scenario.log"
        exit 1
        ;;
esac

# Build YCSB parameters based on system
if [ "$SYSTEM" = "redis" ]; then
    YCSB_PARAMS="-p $ENDPOINT_PARAM=\"$ENDPOINTS\" -p redis.mode=cluster \
        -p redis.datatype=string"
else
    YCSB_PARAMS="-p $ENDPOINT_PARAM=\"$ENDPOINTS\""
fi

# Setup port forwarding for local access
echo "[baseline] Setting up port forwarding..." | tee -a "$OUTPUT_DIR/scenario.log"

if [ "$SYSTEM" = "acp" ]; then
    kubectl port-forward acp-node-0 8080:8080 > /dev/null 2>&1 &
    PF_PID1=$!
    kubectl port-forward acp-node-1 8081:8080 > /dev/null 2>&1 &
    PF_PID2=$!
    kubectl port-forward acp-node-2 8082:8080 > /dev/null 2>&1 &
    PF_PID3=$!

    # Wait for port forwarding to be ready and test connectivity
    echo "[baseline] Waiting for cluster to be ready..." | tee -a "$OUTPUT_DIR/scenario.log"
    sleep 5

    # Test connectivity with a simple operation (retry up to 30 seconds)
    for i in {1..6}; do
        if run_ycsb load $DB_NAME -p $ENDPOINT_PARAM="$ENDPOINTS" -p recordcount=1 > /dev/null 2>&1; then
            echo "[baseline] Cluster connectivity confirmed" | tee -a "$OUTPUT_DIR/scenario.log"
            break
        fi
        if [ $i -eq 6 ]; then
            echo "ERROR: Failed to connect to cluster after 30s" | tee -a "$OUTPUT_DIR/scenario.log"
            exit 1
        fi
        echo "[baseline] Retry $i/6..." | tee -a "$OUTPUT_DIR/scenario.log"
        sleep 5
    done
elif [ "$SYSTEM" = "redis" ]; then
    # Test connectivity (no port-forward needed for in-cluster execution)
    echo "[baseline] Testing Redis connectivity..." | tee -a "$OUTPUT_DIR/scenario.log"
    if ! run_ycsb load $DB_NAME -p $ENDPOINT_PARAM="$ENDPOINTS" -p redis.mode=cluster \
        -p redis.datatype=string -p recordcount=1 -p threadcount=1 > /dev/null 2>&1; then
        echo "ERROR: Failed to connect to Redis" | tee -a "$OUTPUT_DIR/scenario.log"
        exit 1
    fi
    echo "[baseline] Redis connectivity confirmed" | tee -a "$OUTPUT_DIR/scenario.log"
elif [ "$SYSTEM" = "etcd" ]; then
    kubectl port-forward etcd-bench-0 2379:2379 > /dev/null 2>&1 &
    PF_PID1=$!

    # Wait for port forwarding to be ready and test connectivity
    echo "[baseline] Waiting for etcd cluster to be ready..." | tee -a "$OUTPUT_DIR/scenario.log"
    sleep 3

    # Test connectivity
    for i in {1..6}; do
        if run_ycsb load $DB_NAME -p $ENDPOINT_PARAM="$ENDPOINTS" -p recordcount=1 > /dev/null 2>&1; then
            echo "[baseline] etcd connectivity confirmed" | tee -a "$OUTPUT_DIR/scenario.log"
            break
        fi
        if [ $i -eq 6 ]; then
            echo "ERROR: Failed to connect to etcd after 30s" | tee -a "$OUTPUT_DIR/scenario.log"
            exit 1
        fi
        echo "[baseline] Retry $i/6..." | tee -a "$OUTPUT_DIR/scenario.log"
        sleep 5
    done
fi

# Load data phase
echo "[baseline] Loading data..." | tee -a "$OUTPUT_DIR/scenario.log"

# Set workload path based on execution mode
if [ "$USE_IN_CLUSTER" = "true" ]; then
    WORKLOAD_PATH="/workloads/$WORKLOAD.properties"
else
    WORKLOAD_PATH="$SCRIPT_DIR/../../workloads/$WORKLOAD.properties"
fi

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

echo "[baseline] Data loaded successfully" | tee -a "$OUTPUT_DIR/scenario.log"

# Warm-up phase (20s, not counted towards metrics)
echo "[baseline] Running 20s warm-up to avoid cold-cache bias..." | tee -a "$OUTPUT_DIR/scenario.log"
WARMUP_OPS=$((20 * 1000))

if [ "$SYSTEM" = "redis" ]; then
    run_ycsb run $DB_NAME \
        -P "$WORKLOAD_PATH" \
        -p $ENDPOINT_PARAM="$ENDPOINTS" \
        -p redis.mode=cluster \
        -p redis.datatype=string \
        -p operationcount=$WARMUP_OPS \
        -p threadcount=10 \
        > "$OUTPUT_DIR/warmup.log" 2>&1
else
    run_ycsb run $DB_NAME \
        -P "$WORKLOAD_PATH" \
        -p $ENDPOINT_PARAM="$ENDPOINTS" \
        -p operationcount=$WARMUP_OPS \
        -p threadcount=10 \
        > "$OUTPUT_DIR/warmup.log" 2>&1
fi

echo "[baseline] Warm-up complete, starting measured benchmark..." | tee -a "$OUTPUT_DIR/scenario.log"

# Run benchmark
echo "[baseline] Running benchmark for ${DURATION}s..." | tee -a "$OUTPUT_DIR/scenario.log"

# Record start timestamp for Prometheus metrics collection
METRICS_START=$(date -u +%s)

# Calculate operation count based on duration
# Redis achieves ~20,000 ops/sec, use higher multiplier to ensure full duration
if [ "$SYSTEM" = "redis" ]; then
    OPERATION_COUNT=$((DURATION * 20000))
else
    # Use 5000 ops/sec for ACP and etcd (conservative estimate)
    OPERATION_COUNT=$((DURATION * 5000))
fi

echo "[baseline] Target: ${DURATION}s, Operations: ${OPERATION_COUNT}" | tee -a "$OUTPUT_DIR/scenario.log"

if [ "$SYSTEM" = "redis" ]; then
    run_ycsb run $DB_NAME \
        -P "$WORKLOAD_PATH" \
        -p $ENDPOINT_PARAM="$ENDPOINTS" \
        -p redis.mode=cluster \
        -p redis.datatype=string \
        -p operationcount=$OPERATION_COUNT \
        -p threadcount=10 \
        | tee "$OUTPUT_DIR/run.log"
else
    run_ycsb run $DB_NAME \
        -P "$WORKLOAD_PATH" \
        -p $ENDPOINT_PARAM="$ENDPOINTS" \
        -p operationcount=$OPERATION_COUNT \
        -p threadcount=10 \
        | tee "$OUTPUT_DIR/run.log"
fi

# Record end timestamp
METRICS_END=$(date -u +%s)
ACTUAL_DURATION=$((METRICS_END - METRICS_START))

echo "[baseline] Benchmark completed in ${ACTUAL_DURATION}s" | tee -a "$OUTPUT_DIR/scenario.log"

# Extract metrics
echo "[baseline] Extracting metrics..." | tee -a "$OUTPUT_DIR/scenario.log"
grep -E "\[OVERALL\]|\[READ\]|\[UPDATE\]" "$OUTPUT_DIR/run.log" > "$OUTPUT_DIR/metrics.txt" || true

# Collect Prometheus metrics for ACP
if [ "$SYSTEM" = "acp" ]; then
    echo "[baseline] Collecting Prometheus metrics..." | tee -a "$OUTPUT_DIR/scenario.log"

    # Set up port-forward to Prometheus if not already running
    PROM_CHECK=$(curl -s 'http://localhost:9090/api/v1/query?query=up' 2>&1)
    if [[ "$PROM_CHECK" != *'"status":"success"'* ]]; then
        echo "[baseline] Setting up Prometheus port-forward..." | tee -a "$OUTPUT_DIR/scenario.log"
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

echo "[baseline] Benchmark complete! Results in $OUTPUT_DIR" | tee -a "$OUTPUT_DIR/scenario.log"
