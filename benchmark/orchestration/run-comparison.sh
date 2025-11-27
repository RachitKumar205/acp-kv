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
#   --output-dir DIR            Output directory for results
#   --skip-teardown             Don't tear down clusters after benchmarks
#

set -e  # Exit on error

# Default configuration
SCENARIOS="baseline,degraded,partition,high-load"
SYSTEMS="acp"  # TODO: Add redis,etcd support (requires go-ycsb + unified metrics)
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

# Validate systems (only ACP supported currently)
for system in "${SYSTEM_ARRAY[@]}"; do
    if [[ "$system" != "acp" ]]; then
        echo "ERROR: System '$system' not yet supported"
        echo "Currently only 'acp' is supported. Redis/etcd require additional infrastructure."
        echo "See benchmark/README.md for details."
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
echo "Output: $OUTPUT_DIR"
echo ""

# Get script directory
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
COMPARE_DIR="$SCRIPT_DIR/../compare"

# Function to deploy a system
deploy_system() {
    local system=$1
    echo "[$(date +%T)] Deploying $system..."

    if [[ "$system" != "acp" ]]; then
        echo "Error: Only ACP is currently supported"
        return 1
    fi

    kubectl apply -f "$SCRIPT_DIR/../../k8s/statefulset.yaml"
    kubectl apply -f "$SCRIPT_DIR/../../k8s/services.yaml"

    echo "[$(date +%T)] Waiting for ACP pods to be ready..."
    kubectl wait --for=condition=ready pod -l app=acp-node --timeout=120s

    # Additional stabilization delay
    sleep 10

    echo "[$(date +%T)] ACP deployment complete"
}

# Function to teardown a system
teardown_system() {
    local system=$1
    echo "[$(date +%T)] Tearing down $system..."

    if [[ "$system" != "acp" ]]; then
        echo "Error: Only ACP is currently supported"
        return 1
    fi

    kubectl delete -f "$SCRIPT_DIR/../../k8s/statefulset.yaml" --ignore-not-found=true
    kubectl delete -f "$SCRIPT_DIR/../../k8s/services.yaml" --ignore-not-found=true

    # Wait for pods to be deleted
    sleep 5
}

# Function to run benchmark on a system
run_benchmark() {
    local system=$1
    local scenario=$2
    local output_file=$3

    echo "[$(date +%T)] Running $scenario benchmark on $system..."

    # Create system output directory
    local system_output_dir="$OUTPUT_DIR/$system"
    mkdir -p "$system_output_dir"

    # Create scenario-specific output directory
    local scenario_output="$system_output_dir/$scenario"
    mkdir -p "$scenario_output"

    # Run scenario-specific benchmark (pass output dir as 3rd param)
    case $scenario in
        baseline)
            "$SCRIPT_DIR/scenarios/baseline.sh" 120 workloada "$scenario_output" > "$system_output_dir/baseline.log" 2>&1
            ;;
        degraded)
            "$SCRIPT_DIR/scenarios/degraded-latency.sh" 180 workloada "$scenario_output" > "$system_output_dir/degraded.log" 2>&1
            ;;
        partition)
            "$SCRIPT_DIR/scenarios/partition.sh" 240 workloada "$scenario_output" > "$system_output_dir/partition.log" 2>&1
            ;;
        high-load)
            "$SCRIPT_DIR/scenarios/high-load.sh" 120 workloada "$scenario_output" > "$system_output_dir/high-load.log" 2>&1
            ;;
        *)
            echo "Error: Unknown scenario: $scenario"
            return 1
            ;;
    esac

    # Export metrics to CSV
    echo "[$(date +%T)] Exporting metrics for $system/$scenario..."

    if [[ "$system" != "acp" ]]; then
        echo "Error: Only ACP metrics export is currently supported"
        return 1
    fi

    # ACP exposes Prometheus metrics on each node
    echo "[$(date +%T)] Starting port-forward to ACP node..."
    kubectl port-forward acp-node-0 9090:9090 > /dev/null 2>&1 &
    local pf_pid=$!
    sleep 3

    # Export using Prometheus export tool
    cd "$SCRIPT_DIR/../analysis"

    # macOS-compatible date commands
    local start_time=$(date -u -v-5M +%Y-%m-%dT%H:%M:%SZ)
    local end_time=$(date -u +%Y-%m-%dT%H:%M:%SZ)

    go run export-prometheus.go \
        --prometheus-url="http://localhost:9090" \
        --output="$output_file" \
        --start="$start_time" \
        --end="$end_time" || true  # Ignore exit code, check if file was created

    # Kill port-forward
    if [ -n "$pf_pid" ]; then
        kill $pf_pid 2>/dev/null || true
    fi

    echo "[$(date +%T)] Benchmark complete: $output_file"
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

    # Run each scenario
    for scenario in "${SCENARIO_ARRAY[@]}"; do
        output_file="$OUTPUT_DIR/$system/$scenario.csv"
        run_benchmark "$system" "$scenario" "$output_file"

        # Brief cooldown between scenarios
        sleep 5
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
