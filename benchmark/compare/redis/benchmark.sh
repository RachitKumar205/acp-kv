#!/bin/bash
# benchmark.sh - Run YCSB benchmark against Redis cluster
# Usage: ./benchmark.sh [workload] [output-dir]

set -e

WORKLOAD=${1:-workloada}
OUTPUT_DIR=${2:-results/redis}

echo "running YCSB benchmark against Redis"
echo "  workload: $WORKLOAD"
echo "  output: $OUTPUT_DIR"

# create output directory
mkdir -p "$OUTPUT_DIR"

# check if redis is accessible
if ! redis-cli -h redis-bench-0.redis-headless ping &>/dev/null; then
    echo "error: redis cluster not accessible"
    echo "make sure redis is deployed: kubectl apply -f statefulset.yaml"
    exit 1
fi

# redis endpoints
REDIS_ENDPOINTS="redis-bench-0.redis-headless:6379,redis-bench-1.redis-headless:6379,redis-bench-2.redis-headless:6379"

echo "loading data..."
go-ycsb load redis \
    -P "../../workloads/$WORKLOAD" \
    -p redis.addr="$REDIS_ENDPOINTS" \
    -p redis.mode=cluster \
    > "$OUTPUT_DIR/load.log" 2>&1

echo "running benchmark..."
go-ycsb run redis \
    -P "../../workloads/$WORKLOAD" \
    -p redis.addr="$REDIS_ENDPOINTS" \
    -p redis.mode=cluster \
    | tee "$OUTPUT_DIR/run.log"

# extract metrics
echo "extracting metrics..."
grep -E "(READ|UPDATE|INSERT)" "$OUTPUT_DIR/run.log" > "$OUTPUT_DIR/metrics.txt" || true

echo "benchmark complete! results in $OUTPUT_DIR"
