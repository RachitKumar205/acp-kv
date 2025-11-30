#!/bin/bash
#
# Run Comparison Benchmarks
#
# Executes identical workloads on ACP, Redis, and etcd to generate fair comparison data.
# Each system is deployed, benchmarked, and torn down sequentially to avoid resource contention.
#
# Usage:
#   ./run-comparison.sh [options]
#
# Options:
#   --scenarios SCENARIO_LIST   Comma-separated scenarios (baseline,degraded,partition,high-load)
#   --systems SYSTEM_LIST       Comma-separated systems (acp,redis,etcd)
#   --workloads WORKLOAD_LIST   Comma-separated workloads (workloada,workloadb,workloadc)
#   --output-dir DIR            Output directory for results
#   --skip-teardown             Don't tear down clusters after benchmarks
#

set -e  # Exit on error

# Default configuration
SCENARIOS="baseline,degraded,partition,high-load"
SYSTEMS="acp"  # TODO: Add redis,etcd support (requires go-ycsb + unified metrics)
WORKLOADS="workloada,workloadb"  # Test both write-heavy and read-heavy for rigor
OUTPUT_DIR="../results/comparison"
SKIP_TEARDOWN=false

# Parse arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        --scenarios)
            SCENARIOS="$2"
            shift 2
            ;;
        --systems)
            SYSTEMS="$2"
            shift 2
            ;;
        --workloads)
            WORKLOADS="$2"
            shift 2
            ;;
        --output-dir)
            OUTPUT_DIR="$2"
            shift 2
            ;;
        --skip-teardown)
            SKIP_TEARDOWN=true
            shift
            ;;
        *)
            echo "Unknown option: $1"
            exit 1
            ;;
    esac
done

# Convert comma-separated strings to arrays
IFS=',' read -r -a SCENARIO_ARRAY <<< "$SCENARIOS"
IFS=',' read -r -a SYSTEM_ARRAY <<< "$SYSTEMS"
IFS=',' read -r -a WORKLOAD_ARRAY <<< "$WORKLOADS"

# Validate systems
for system in "${SYSTEM_ARRAY[@]}"; do
    if [[ "$system" != "acp" && "$system" != "redis" && "$system" != "etcd" ]]; then
        echo "ERROR: Unknown system '$system'"
        echo "Supported systems: acp, redis, etcd"
        exit 1
    fi
done

# Create output directory
mkdir -p "$OUTPUT_DIR"

echo "=========================================="
echo "ACP COMPARISON BENCHMARK SUITE"
echo "=========================================="
echo "Scenarios: ${SCENARIO_ARRAY[*]}"
echo "Systems: ${SYSTEM_ARRAY[*]}"
echo "Workloads: ${WORKLOAD_ARRAY[*]}"
echo "Output: $OUTPUT_DIR"
echo ""

# Get script directory
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
COMPARE_DIR="$SCRIPT_DIR/../compare"

# Function to deploy a system
deploy_system() {
    local system=$1
    echo "[$(date +%T)] Deploying $system..."

    case $system in
        acp)
            # Apply services first so DNS is available when pods start
            kubectl apply -f "$SCRIPT_DIR/../../k8s/services.yaml"
            kubectl apply -f "$SCRIPT_DIR/../../k8s/statefulset.yaml"
            echo "[$(date +%T)] Waiting for ACP pods to be ready..."
            kubectl wait --for=condition=ready pod -l app=acp-node --timeout=120s
            # Extra wait for inter-node connections to establish
            sleep 15
            echo "[$(date +%T)] ACP deployment complete"
            ;;
        redis)
            kubectl apply -f "$COMPARE_DIR/redis/statefulset.yaml"
            echo "[$(date +%T)] Waiting for Redis pods to be ready..."
            kubectl wait --for=condition=ready pod -l app=redis-bench --timeout=120s
            sleep 5
            echo "[$(date +%T)] Initializing Redis cluster..."
            "$COMPARE_DIR/redis/init-cluster.sh"
            echo "[$(date +%T)] Redis deployment complete"
            ;;
        etcd)
            kubectl apply -f "$COMPARE_DIR/etcd/statefulset.yaml"
            echo "[$(date +%T)] Waiting for etcd pods to be ready..."
            kubectl wait --for=condition=ready pod -l app=etcd-bench --timeout=120s
            sleep 10
            echo "[$(date +%T)] etcd deployment complete"
            ;;
        *)
            echo "Error: Unknown system: $system"
            return 1
            ;;
    esac
}

# Function to teardown a system
teardown_system() {
    local system=$1
    echo "[$(date +%T)] Tearing down $system..."

    case $system in
        acp)
            kubectl delete -f "$SCRIPT_DIR/../../k8s/statefulset.yaml" --ignore-not-found=true
            kubectl delete -f "$SCRIPT_DIR/../../k8s/services.yaml" --ignore-not-found=true
            ;;
        redis)
            kubectl delete -f "$COMPARE_DIR/redis/statefulset.yaml" --ignore-not-found=true
            ;;
        etcd)
            kubectl delete -f "$COMPARE_DIR/etcd/statefulset.yaml" --ignore-not-found=true
            ;;
        *)
            echo "Error: Unknown system: $system"
            return 1
            ;;
    esac

    # Wait for pods to be deleted
    sleep 5
}

# Function to run benchmark on a system
run_benchmark() {
    local system=$1
    local scenario=$2
    local workload=$3
    local output_file=$4

    echo "[$(date +%T)] Running $scenario benchmark on $system with $workload..."

    # Create system output directory
    local system_output_dir="$OUTPUT_DIR/$system"
    mkdir -p "$system_output_dir"

    # Create scenario-specific output directory (include workload in path)
    local scenario_output="$system_output_dir/$scenario-$workload"
    mkdir -p "$scenario_output"

    # Run YCSB scenario (pass system as first param, workload as third)
    case $scenario in
        baseline)
            "$SCRIPT_DIR/scenarios-ycsb/baseline.sh" "$system" 120 "$workload" "$scenario_output" > "$system_output_dir/$scenario-$workload.log" 2>&1
            ;;
        degraded)
            "$SCRIPT_DIR/scenarios-ycsb/degraded.sh" "$system" 180 "$workload" "$scenario_output" > "$system_output_dir/$scenario-$workload.log" 2>&1
            ;;
        partition)
            "$SCRIPT_DIR/scenarios-ycsb/partition.sh" "$system" 240 "$workload" "$scenario_output" > "$system_output_dir/$scenario-$workload.log" 2>&1
            ;;
        high-load)
            "$SCRIPT_DIR/scenarios-ycsb/high-load.sh" "$system" 120 "$workload" "$scenario_output" > "$system_output_dir/$scenario-$workload.log" 2>&1
            ;;
        *)
            echo "Error: Unknown scenario '$scenario'"
            echo "Supported scenarios: baseline, degraded, partition, high-load"
            return 1
            ;;
    esac

    # Copy YCSB metrics to output file
    echo "[$(date +%T)] Copying metrics for $system/$scenario/$workload..."

    if [ -f "$scenario_output/metrics.txt" ]; then
        cp "$scenario_output/metrics.txt" "$output_file"
        echo "[$(date +%T)] Metrics saved to: $output_file"
    else
        echo "[$(date +%T)] Warning: No metrics file found at $scenario_output/metrics.txt"
    fi

    echo "[$(date +%T)] Benchmark complete"
}

# Main execution loop
START_TIME=$(date +%s)

for system in "${SYSTEM_ARRAY[@]}"; do
    echo ""
    echo "=========================================="
    echo "SYSTEM: $system"
    echo "=========================================="

    # Deploy system
    deploy_system "$system"

    # Run each scenario with each workload
    for scenario in "${SCENARIO_ARRAY[@]}"; do
        for workload in "${WORKLOAD_ARRAY[@]}"; do
            output_file="$OUTPUT_DIR/$system/$scenario-$workload.csv"
            run_benchmark "$system" "$scenario" "$workload" "$output_file"

            # Brief cooldown between runs
            sleep 5
        done
    done

    # Teardown (unless skipped)
    if [ "$SKIP_TEARDOWN" = false ]; then
        teardown_system "$system"
    else
        echo "[$(date +%T)] Skipping teardown (--skip-teardown enabled)"
    fi

    echo "[$(date +%T)] $system benchmarks complete"
done

END_TIME=$(date +%s)
DURATION=$((END_TIME - START_TIME))

echo ""
echo "=========================================="
echo "COMPARISON BENCHMARK COMPLETE"
echo "=========================================="
echo "Duration: ${DURATION}s"
echo "Results: $OUTPUT_DIR"
echo ""
echo "Output structure:"
tree "$OUTPUT_DIR" 2>/dev/null || find "$OUTPUT_DIR" -type f

echo ""
echo "Next steps:"
echo "  1. Review logs in $OUTPUT_DIR/*/[scenario].log"
echo "  2. Generate figures:"
echo "     cd ../analysis/paper_figures"
echo "     python generate_all.py --data-dir $OUTPUT_DIR --output-dir ./figures"
