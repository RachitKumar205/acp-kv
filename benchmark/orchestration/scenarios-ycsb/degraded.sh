#!/bin/bash
# degraded.sh - YCSB degraded node scenario
# Usage: ./degraded.sh <system> <duration> <workload> <output_dir>
#
# Tests performance when one node fails mid-benchmark
# Phase 1 (0-60s): Normal operation
# Phase 2 (60-120s): One node killed
# Phase 3 (120-180s): Recovery period

set -e

SYSTEM=${1:-acp}
DURATION=${2:-180}
WORKLOAD=${3:-workloada}
OUTPUT_DIR=${4:-results/degraded}

echo "=== YCSB Degraded Node Benchmark ===" | tee "$OUTPUT_DIR/scenario.log"
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
        POD_NAME="acp-node-1"
        ;;
    redis)
        DB_NAME="redis"
        ENDPOINTS="redis-bench-0.redis-headless:6379"
        ENDPOINT_PARAM="redis.addr"
        POD_NAME="redis-bench-1"
        USE_IN_CLUSTER=true
        ;;
    etcd)
        DB_NAME="etcd"
        ENDPOINTS="localhost:2379"
        ENDPOINT_PARAM="etcd.endpoints"
        POD_NAME="etcd-bench-1"
        ;;
    *)
        echo "ERROR: Unknown system: $SYSTEM" | tee -a "$OUTPUT_DIR/scenario.log"
        exit 1
        ;;
esac

# Setup port forwarding (skip for in-cluster Redis)
if [ "$SYSTEM" = "acp" ]; then
    echo "[degraded] Setting up port forwarding..." | tee -a "$OUTPUT_DIR/scenario.log"
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

# Load data phase
echo "[degraded] Loading data..." | tee -a "$OUTPUT_DIR/scenario.log"

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

echo "[degraded] Data loaded successfully" | tee -a "$OUTPUT_DIR/scenario.log"

# Calculate phase durations
PHASE1_DURATION=60
FAILURE_DURATION=60
RECOVERY_DURATION=$((DURATION - PHASE1_DURATION - FAILURE_DURATION))

if [ $RECOVERY_DURATION -lt 0 ]; then
    RECOVERY_DURATION=60
    TOTAL_DURATION=$((PHASE1_DURATION + FAILURE_DURATION + RECOVERY_DURATION))
    echo "[degraded] Adjusted total duration to ${TOTAL_DURATION}s for 3 phases" | tee -a "$OUTPUT_DIR/scenario.log"
    DURATION=$TOTAL_DURATION
fi

# Start benchmark in background
echo "[degraded] Starting benchmark (${DURATION}s total)..." | tee -a "$OUTPUT_DIR/scenario.log"

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

echo "[degraded] Target: ${DURATION}s, Operations: ${OPERATION_COUNT}" | tee -a "$OUTPUT_DIR/scenario.log"

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
echo "[degraded] Phase 1: Normal operation (${PHASE1_DURATION}s)..." | tee -a "$OUTPUT_DIR/scenario.log"
sleep $PHASE1_DURATION

# Phase 2: Kill leader/primary node
echo "[degraded] Phase 2: Detecting and killing leader/primary..." | tee -a "$OUTPUT_DIR/scenario.log"

# Determine which node to kill based on system type
case $SYSTEM in
    etcd)
        # For etcd (CP system), find and kill the leader
        echo "[degraded] Detecting etcd leader..." | tee -a "$OUTPUT_DIR/scenario.log"
        LEADER_POD=$(kubectl exec etcd-bench-0 -- etcdctl --endpoints=localhost:2379 endpoint status --cluster -w json 2>/dev/null | grep -o '"Endpoint":"[^"]*","Status":{"header":{"cluster_id":[^}]*,"member_id":[^,]*,"revision":[^,]*,"raft_term":[^}]*},"leader":[0-9]*' | grep '"leader":[1-9]' | head -1 | grep -o 'etcd-bench-[0-9]' || echo "etcd-bench-1")
        echo "[degraded] Killing etcd leader: $LEADER_POD" | tee -a "$OUTPUT_DIR/scenario.log"
        kubectl delete pod $LEADER_POD --grace-period=0 --force 2>&1 | tee -a "$OUTPUT_DIR/scenario.log" || true
        ;;
    acp)
        # For ACP (CP system), kill the primary node (for now, assume node-0 or detect via metrics)
        # TODO: Add primary detection via Prometheus metrics if available
        echo "[degraded] Killing ACP primary (assuming acp-node-0)..." | tee -a "$OUTPUT_DIR/scenario.log"
        kubectl delete pod acp-node-0 --grace-period=0 --force 2>&1 | tee -a "$OUTPUT_DIR/scenario.log" || true
        ;;
    redis)
        # For Redis (AP system), kill a replica (NOT the master)
        echo "[degraded] Detecting Redis replica..." | tee -a "$OUTPUT_DIR/scenario.log"
        # Find a replica node (not master)
        for i in 0 1 2; do
            ROLE=$(kubectl exec redis-bench-$i -- redis-cli role 2>/dev/null | head -1 || echo "")
            if [ "$ROLE" = "slave" ]; then
                REPLICA_POD="redis-bench-$i"
                break
            fi
        done
        # Fallback if no replica found (cluster might not have replicas in current config)
        REPLICA_POD=${REPLICA_POD:-redis-bench-2}
        echo "[degraded] Killing Redis replica: $REPLICA_POD" | tee -a "$OUTPUT_DIR/scenario.log"
        kubectl delete pod $REPLICA_POD --grace-period=0 --force 2>&1 | tee -a "$OUTPUT_DIR/scenario.log" || true
        ;;
esac

echo "[degraded] Node killed, waiting ${FAILURE_DURATION}s..." | tee -a "$OUTPUT_DIR/scenario.log"
FAILURE_TIMESTAMP=$(date +%s)

sleep $FAILURE_DURATION

# Phase 3: Recovery
echo "[degraded] Phase 3: Recovery period (${RECOVERY_DURATION}s)..." | tee -a "$OUTPUT_DIR/scenario.log"
echo "[degraded] Detecting recovery..." | tee -a "$OUTPUT_DIR/scenario.log"

# Detect when cluster returns to full capacity
RECOVERY_DETECTED=false
RECOVERY_START=$(date +%s)
MAX_RECOVERY_WAIT=120

for i in $(seq 1 $MAX_RECOVERY_WAIT); do
    case $SYSTEM in
        acp)
            RUNNING_COUNT=$(kubectl get pods -l app=acp-node --no-headers 2>/dev/null | grep "Running" | grep "1/1" | wc -l || echo "0")
            EXPECTED_COUNT=3
            ;;
        etcd)
            RUNNING_COUNT=$(kubectl get pods -l app=etcd-bench --no-headers 2>/dev/null | grep "Running" | grep "1/1" | wc -l || echo "0")
            EXPECTED_COUNT=3
            ;;
        redis)
            RUNNING_COUNT=$(kubectl get pods -l app=redis-bench --no-headers 2>/dev/null | grep "Running" | grep "1/1" | wc -l || echo "0")
            EXPECTED_COUNT=3
            ;;
    esac

    if [ "$RUNNING_COUNT" -ge "$EXPECTED_COUNT" ]; then
        RECOVERY_TIMESTAMP=$(date +%s)
        RECOVERY_TIME=$((RECOVERY_TIMESTAMP - FAILURE_TIMESTAMP))
        echo "[degraded] Recovery detected at ${RECOVERY_TIME}s after failure" | tee -a "$OUTPUT_DIR/scenario.log"
        echo "Recovery Time: ${RECOVERY_TIME}s" >> "$OUTPUT_DIR/metrics.txt"
        RECOVERY_DETECTED=true
        break
    fi
    sleep 1
done

if [ "$RECOVERY_DETECTED" = "false" ]; then
    echo "[degraded] Warning: Full recovery not detected within ${MAX_RECOVERY_WAIT}s" | tee -a "$OUTPUT_DIR/scenario.log"
    echo "Recovery Time: >$MAX_RECOVERY_WAIT}s (incomplete)" >> "$OUTPUT_DIR/metrics.txt"
fi

# Wait for remainder of recovery period
ELAPSED_RECOVERY=$(($(date +%s) - RECOVERY_START))
REMAINING_RECOVERY=$((RECOVERY_DURATION - ELAPSED_RECOVERY))
if [ $REMAINING_RECOVERY -gt 0 ]; then
    echo "[degraded] Waiting ${REMAINING_RECOVERY}s for system stabilization..." | tee -a "$OUTPUT_DIR/scenario.log"
    sleep $REMAINING_RECOVERY
fi

# Wait for YCSB to complete
echo "[degraded] Waiting for benchmark to complete..." | tee -a "$OUTPUT_DIR/scenario.log"
wait $YCSB_PID 2>/dev/null || true

# Record end timestamp
METRICS_END=$(date -u +%s)
ACTUAL_DURATION=$((METRICS_END - METRICS_START))

echo "[degraded] Benchmark completed in ${ACTUAL_DURATION}s" | tee -a "$OUTPUT_DIR/scenario.log"

# Extract metrics
echo "[degraded] Extracting metrics..." | tee -a "$OUTPUT_DIR/scenario.log"
grep -E "\[OVERALL\]|\[READ\]|\[UPDATE\]" "$OUTPUT_DIR/run.log" > "$OUTPUT_DIR/metrics.txt" || true

# Collect Prometheus metrics for ACP
if [ "$SYSTEM" = "acp" ]; then
    echo "[degraded] Collecting Prometheus metrics..." | tee -a "$OUTPUT_DIR/scenario.log"

    # Set up port-forward to Prometheus if not already running
    PROM_CHECK=$(curl -s 'http://localhost:9090/api/v1/query?query=up' 2>&1)
    if [[ "$PROM_CHECK" != *'"status":"success"'* ]]; then
        echo "[degraded] Setting up Prometheus port-forward..." | tee -a "$OUTPUT_DIR/scenario.log"
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

echo "[degraded] Benchmark complete! Results in $OUTPUT_DIR" | tee -a "$OUTPUT_DIR/scenario.log"
