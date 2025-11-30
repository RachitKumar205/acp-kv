#!/bin/bash
# partition.sh - YCSB network partition scenario
# Usage: ./partition.sh <system> <duration> <workload> <output_dir>
#
# Tests split-brain tolerance and reconciliation
# Phase 1 (0-30s): Normal operation
# Phase 2 (30-150s): Network partition (node-2 isolated)
# Phase 3 (150-240s): Partition healed, reconciliation

set -e

SYSTEM=${1:-acp}
DURATION=${2:-240}
WORKLOAD=${3:-workloada}
OUTPUT_DIR=${4:-results/partition}

echo "=== YCSB Network Partition Benchmark ===" | tee "$OUTPUT_DIR/scenario.log"
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
        PARTITION_POD="acp-node-2"
        ;;
    redis)
        DB_NAME="redis"
        ENDPOINTS="redis-bench-0.redis-headless:6379"
        ENDPOINT_PARAM="redis.addr"
        PARTITION_POD="redis-bench-2"
        USE_IN_CLUSTER=true
        ;;
    etcd)
        DB_NAME="etcd"
        ENDPOINTS="localhost:2379"
        ENDPOINT_PARAM="etcd.endpoints"
        PARTITION_POD="etcd-bench-2"
        ;;
    *)
        echo "ERROR: Unknown system: $SYSTEM" | tee -a "$OUTPUT_DIR/scenario.log"
        exit 1
        ;;
esac

# Setup port forwarding (skip for in-cluster Redis)
if [ "$SYSTEM" = "acp" ]; then
    echo "[partition] Setting up port forwarding..." | tee -a "$OUTPUT_DIR/scenario.log"
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
echo "[partition] Loading data..." | tee -a "$OUTPUT_DIR/scenario.log"
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

echo "[partition] Data loaded successfully" | tee -a "$OUTPUT_DIR/scenario.log"

# Calculate phase durations
PHASE1_DURATION=30
PARTITION_DURATION=120
HEAL_DURATION=$((DURATION - PHASE1_DURATION - PARTITION_DURATION))

if [ $HEAL_DURATION -lt 0 ]; then
    HEAL_DURATION=60
    TOTAL_DURATION=$((PHASE1_DURATION + PARTITION_DURATION + HEAL_DURATION))
    echo "[partition] Adjusted total duration to ${TOTAL_DURATION}s for 3 phases" | tee -a "$OUTPUT_DIR/scenario.log"
    DURATION=$TOTAL_DURATION
fi

# Start benchmark in background
echo "[partition] Starting benchmark (${DURATION}s total)..." | tee -a "$OUTPUT_DIR/scenario.log"

# Record start timestamp for Prometheus metrics collection
METRICS_START=$(date -u +%s)

# Calculate operation count based on duration
# Use 5000 ops/sec to account for high-performance clusters
# Calculate operation count
if [ "$SYSTEM" = "redis" ]; then
    OPERATION_COUNT=$((DURATION * 20000))
else
    OPERATION_COUNT=$((DURATION * 5000))
fi

echo "[partition] Target: ${DURATION}s, Operations: ${OPERATION_COUNT}" | tee -a "$OUTPUT_DIR/scenario.log"

if [ "$SYSTEM" = "redis" ]; then
    run_ycsb run $DB_NAME \
        -P "$WORKLOAD_PATH" \
        -p $ENDPOINT_PARAM="$ENDPOINTS" \
        -p redis.mode=cluster \
        -p redis.datatype=string \
        -p operationcount=$OPERATION_COUNT \
        -p threadcount=10 \
        > "$OUTPUT_DIR/run.log" 2>&1 &
    YCSB_PID=$!
else
    run_ycsb run $DB_NAME \
        -P "$WORKLOAD_PATH" \
        -p $ENDPOINT_PARAM="$ENDPOINTS" \
        -p operationcount=$OPERATION_COUNT \
        -p threadcount=10 \
        > "$OUTPUT_DIR/run.log" 2>&1 &
    YCSB_PID=$!
fi

# Phase 1: Normal operation
echo "[partition] Phase 1: Normal operation (${PHASE1_DURATION}s)..." | tee -a "$OUTPUT_DIR/scenario.log"
sleep $PHASE1_DURATION

# Phase 2: Create network partition
echo "[partition] Phase 2: Creating network partition on $PARTITION_POD (${PARTITION_DURATION}s)..." | tee -a "$OUTPUT_DIR/scenario.log"

# Install tc (traffic control) if not present
if [ "$SYSTEM" = "redis" ]; then
    echo "[partition] Installing iproute2 (tc) in Redis container..." | tee -a "$OUTPUT_DIR/scenario.log"
    kubectl exec $PARTITION_POD -- apk add --no-cache iproute2 > /dev/null 2>&1 || true
    # Inject network delay/packet loss to simulate partition
    kubectl exec $PARTITION_POD -- tc qdisc add dev eth0 root netem loss 100% 2>&1 | tee -a "$OUTPUT_DIR/scenario.log" || true
elif [ "$SYSTEM" = "etcd" ]; then
    # etcd container is minimal, use kubectl debug with network tools
    echo "[partition] Using debug container to apply tc rules..." | tee -a "$OUTPUT_DIR/scenario.log"
    kubectl debug $PARTITION_POD --image=nicolaka/netshoot --target=etcd --profile=netadmin --stdin=false --tty=false -- tc qdisc add dev eth0 root netem loss 100% 2>&1 | tee -a "$OUTPUT_DIR/scenario.log" || true
else
    # For ACP
    kubectl exec $PARTITION_POD -- tc qdisc add dev eth0 root netem loss 100% 2>&1 | tee -a "$OUTPUT_DIR/scenario.log" || true
fi

sleep $PARTITION_DURATION

# Phase 3: Heal partition
echo "[partition] Phase 3: Healing partition (${HEAL_DURATION}s)..." | tee -a "$OUTPUT_DIR/scenario.log"

# Remove network impairment
if [ "$SYSTEM" = "etcd" ]; then
    kubectl debug $PARTITION_POD --image=nicolaka/netshoot --target=etcd --profile=netadmin --stdin=false --tty=false -- tc qdisc del dev eth0 root 2>&1 | tee -a "$OUTPUT_DIR/scenario.log" || true
else
    kubectl exec $PARTITION_POD -- tc qdisc del dev eth0 root 2>&1 | tee -a "$OUTPUT_DIR/scenario.log" || true
fi

# Record healing timestamp
HEAL_TIMESTAMP=$(date +%s)
echo "[partition] Partition healed at $(date -u +%H:%M:%S)" | tee -a "$OUTPUT_DIR/scenario.log"

# For ACP, track reconciliation activity
if [ "$SYSTEM" = "acp" ]; then
    echo "[partition] Monitoring reconciliation activity..." | tee -a "$OUTPUT_DIR/scenario.log"

    # Wait a moment for reconciliation to start
    sleep 5

    # Check for reconciliation runs (if metrics endpoint is accessible)
    # This is a best-effort measurement
    echo "[partition] Reconciliation window: ${HEAL_DURATION}s" | tee -a "$OUTPUT_DIR/scenario.log"
fi

echo "[partition] Waiting ${HEAL_DURATION}s for system stabilization..." | tee -a "$OUTPUT_DIR/scenario.log"
sleep $HEAL_DURATION

# Calculate total reconciliation window
RECONCILIATION_END=$(date +%s)
RECONCILIATION_TIME=$((RECONCILIATION_END - HEAL_TIMESTAMP))
echo "Reconciliation Time: ${RECONCILIATION_TIME}s" >> "$OUTPUT_DIR/metrics.txt"

# Wait for YCSB to complete
echo "[partition] Waiting for benchmark to complete..." | tee -a "$OUTPUT_DIR/scenario.log"
wait $YCSB_PID 2>/dev/null || true

# Record end timestamp
METRICS_END=$(date -u +%s)
ACTUAL_DURATION=$((METRICS_END - METRICS_START))

echo "[partition] Benchmark completed in ${ACTUAL_DURATION}s" | tee -a "$OUTPUT_DIR/scenario.log"

# Extract metrics
echo "[partition] Extracting metrics..." | tee -a "$OUTPUT_DIR/scenario.log"
grep -E "\[OVERALL\]|\[READ\]|\[UPDATE\]" "$OUTPUT_DIR/run.log" > "$OUTPUT_DIR/metrics.txt" || true

# Collect Prometheus metrics for ACP
if [ "$SYSTEM" = "acp" ]; then
    echo "[partition] Collecting Prometheus metrics..." | tee -a "$OUTPUT_DIR/scenario.log"

    # Set up port-forward to Prometheus if not already running
    PROM_CHECK=$(curl -s 'http://localhost:9090/api/v1/query?query=up' 2>&1)
    if [[ "$PROM_CHECK" != *'"status":"success"'* ]]; then
        echo "[partition] Setting up Prometheus port-forward..." | tee -a "$OUTPUT_DIR/scenario.log"
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

# Analyze partition-specific metrics
echo "" | tee -a "$OUTPUT_DIR/metrics.txt"
echo "=== PARTITION IMPACT METRICS ===" | tee -a "$OUTPUT_DIR/metrics.txt"

# 1. Count failed operations (errors during partition)
FAILED_OPS=$(grep -i "error\|failed\|timeout" "$OUTPUT_DIR/run.log" | wc -l || echo "0")
echo "Failed Operations: $FAILED_OPS" | tee -a "$OUTPUT_DIR/metrics.txt"

# 2. Estimate unavailability window (operations with errors during partition phase)
UNAVAILABLE_DURATION="~0-${PARTITION_DURATION}s (partition window)"
echo "Potential Unavailability: $UNAVAILABLE_DURATION" | tee -a "$OUTPUT_DIR/metrics.txt"

# 3. System-specific metrics
case $SYSTEM in
    etcd|acp)
        # For CP systems, count rejected writes
        REJECTED_WRITES=$(grep -i "write.*reject\|write.*fail\|write.*error" "$OUTPUT_DIR/run.log" | wc -l || echo "0")
        echo "Rejected Writes (CP system): $REJECTED_WRITES" | tee -a "$OUTPUT_DIR/metrics.txt"

        if [ "$SYSTEM" = "acp" ]; then
            # For ACP specifically, try to extract staleness from logs/metrics
            echo "Staleness: Check ACP Prometheus metrics (acp_staleness_seconds)" | tee -a "$OUTPUT_DIR/metrics.txt"
        fi
        ;;
    redis)
        # For AP system, note that writes continued (eventual consistency)
        echo "AP System Note: Writes accepted with eventual consistency" | tee -a "$OUTPUT_DIR/metrics.txt"
        ;;
esac

echo "" | tee -a "$OUTPUT_DIR/metrics.txt"
echo "See run.log for detailed error timeline" | tee -a "$OUTPUT_DIR/metrics.txt"

# Cleanup port forwarding
if [ "$SYSTEM" = "acp" ]; then
    if [ -n "$PF_PID1" ]; then kill $PF_PID1 2>/dev/null || true; fi
    if [ -n "$PF_PID2" ]; then kill $PF_PID2 2>/dev/null || true; fi
    if [ -n "$PF_PID3" ]; then kill $PF_PID3 2>/dev/null || true; fi
elif [ "$SYSTEM" = "redis" ] || [ "$SYSTEM" = "etcd" ]; then
    if [ -n "$PF_PID1" ]; then kill $PF_PID1 2>/dev/null || true; fi
fi

echo "[partition] Benchmark complete! Results in $OUTPUT_DIR" | tee -a "$OUTPUT_DIR/scenario.log"
