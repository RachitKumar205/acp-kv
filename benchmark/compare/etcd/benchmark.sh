#!/bin/bash
# benchmark.sh - Run YCSB benchmark against etcd cluster
# Usage: ./benchmark.sh [workload] [output-dir]

set -e

WORKLOAD=${1:-workloada}
OUTPUT_DIR=${2:-results/etcd}

echo "running YCSB benchmark against etcd"
echo "  workload: $WORKLOAD"
echo "  output: $OUTPUT_DIR"

# create output directory
mkdir -p "$OUTPUT_DIR"

# check if etcd is accessible
if ! etcdctl --endpoints=http://etcd-bench-0.etcd-headless:2379 endpoint health &>/dev/null; then
    echo "error: etcd cluster not accessible"
    echo "make sure etcd is deployed: kubectl apply -f statefulset.yaml"
    exit 1
fi

# etcd endpoints
ETCD_ENDPOINTS="etcd-bench-0.etcd-headless:2379,etcd-bench-1.etcd-headless:2379,etcd-bench-2.etcd-headless:2379"

echo "loading data..."
go-ycsb load etcd \
    -P "../../workloads/$WORKLOAD" \
    -p etcd.endpoints="$ETCD_ENDPOINTS" \
    > "$OUTPUT_DIR/load.log" 2>&1

echo "running benchmark..."
go-ycsb run etcd \
    -P "../../workloads/$WORKLOAD" \
    -p etcd.endpoints="$ETCD_ENDPOINTS" \
    | tee "$OUTPUT_DIR/run.log"

# extract metrics
echo "extracting metrics..."
grep -E "(READ|UPDATE|INSERT)" "$OUTPUT_DIR/run.log" > "$OUTPUT_DIR/metrics.txt" || true

echo "benchmark complete! results in $OUTPUT_DIR"
