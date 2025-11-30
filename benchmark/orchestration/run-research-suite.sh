#!/bin/bash
# run-research-suite.sh - Master runner for ACP research benchmark suite
# Usage: ./run-research-suite.sh <system> [trial_name]
#
# Runs the complete research suite:
# Test 1: Baseline Performance (YCSB A & B workloads)
# Test 2: Node Failure (degraded cluster)
# Test 3: Network Partition (split-brain & reconciliation)
# Test 4: High-Load Stress Test (ramping load)
#
# system: acp, redis, or etcd
# trial_name: optional identifier (default: research-YYYYMMDD-HHMMSS)

set -e

SYSTEM=${1:-acp}
TRIAL_NAME=${2:-research-$(date +%Y%m%d-%H%M%S)}

# Get script directory
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SCENARIOS_DIR="$SCRIPT_DIR/scenarios-ycsb"

# Results directory structure
BASE_RESULTS_DIR="$SCRIPT_DIR/results/$TRIAL_NAME"
mkdir -p "$BASE_RESULTS_DIR"

echo "========================================"
echo "ACP Research Benchmark Suite"
echo "========================================"
echo "System: $SYSTEM"
echo "Trial: $TRIAL_NAME"
echo "Results: $BASE_RESULTS_DIR"
echo "Start Time: $(date)"
echo "========================================"
echo ""

# Log to both stdout and file
exec > >(tee "$BASE_RESULTS_DIR/suite.log") 2>&1

# Verify scenario scripts exist
if [ ! -f "$SCENARIOS_DIR/baseline.sh" ]; then
    echo "ERROR: Scenario scripts not found in $SCENARIOS_DIR"
    exit 1
fi

# Helper function to run a test with timing
run_test() {
    local test_num=$1
    local test_name=$2
    local script=$3
    shift 3
    local args=("$@")

    echo ""
    echo "========================================"
    echo "Test $test_num: $test_name"
    echo "Time: $(date)"
    echo "========================================"

    TEST_START=$(date +%s)

    # Create test output directory
    TEST_OUTPUT_DIR="$BASE_RESULTS_DIR/$test_num-$test_name"
    mkdir -p "$TEST_OUTPUT_DIR"

    # Run the scenario script
    bash "$SCENARIOS_DIR/$script" "$SYSTEM" "${args[@]}" "$TEST_OUTPUT_DIR" || {
        echo "ERROR: Test $test_num failed"
        exit 1
    }

    TEST_END=$(date +%s)
    TEST_DURATION=$((TEST_END - TEST_START))

    echo ""
    echo "Test $test_num complete in ${TEST_DURATION}s"
    echo "========================================"
}

# Helper function for cooldown periods
cooldown() {
    local duration=$1
    local reason=$2

    echo ""
    echo "Cooldown: ${duration}s ($reason)"
    echo -n "Waiting: "
    for i in $(seq 1 $duration); do
        echo -n "."
        sleep 1
    done
    echo " done"
    echo ""
}

SUITE_START=$(date +%s)

# Test 1a: Baseline Performance - Workload A (50/50 read/update)
run_test "1a" "baseline-workloada" "baseline.sh" 120 "workloada"
cooldown 60 "cluster stabilization"

# Test 1b: Baseline Performance - Workload B (95/5 read/update)
run_test "1b" "baseline-workloadb" "baseline.sh" 120 "workloadb"
cooldown 60 "cluster stabilization"

# Test 2: Node Failure (degraded cluster)
run_test "2" "node-failure" "degraded.sh" 180 "workloada"
cooldown 300 "cluster recovery & stabilization"

# Test 3: Network Partition (split-brain & reconciliation)
run_test "3" "network-partition" "partition.sh" 240 "workloada"
cooldown 300 "partition recovery & reconciliation"

# Test 4: High-Load Stress Test (ramping load)
run_test "4" "high-load-stress" "high-load.sh" 120 "workloada"

SUITE_END=$(date +%s)
SUITE_DURATION=$((SUITE_END - SUITE_START))
SUITE_DURATION_MIN=$((SUITE_DURATION / 60))

echo ""
echo "========================================"
echo "Research Suite Complete!"
echo "========================================"
echo "System: $SYSTEM"
echo "Trial: $TRIAL_NAME"
echo "Total Duration: ${SUITE_DURATION}s (~${SUITE_DURATION_MIN} minutes)"
echo "End Time: $(date)"
echo "Results: $BASE_RESULTS_DIR"
echo ""
echo "Test Results Summary:"
echo "  1a: baseline-workloada (YCSB Workload A: 50/50 read/update)"
echo "  1b: baseline-workloadb (YCSB Workload B: 95/5 read/update)"
echo "  2:  node-failure (degraded cluster with leader kill)"
echo "  3:  network-partition (split-brain & reconciliation)"
echo "  4:  high-load-stress (ramping load: 100→1000→10000 ops/sec)"
echo ""
echo "Next Steps:"
echo "  - Review individual test logs in $BASE_RESULTS_DIR/"
echo "  - Check metrics.txt files for YCSB performance data"
if [ "$SYSTEM" = "acp" ]; then
    echo "  - Analyze prometheus-metrics.csv files for ACP-specific metrics"
    echo "  - Look for:"
    echo "    * Quorum adjustments (acp_current_r, acp_current_w)"
    echo "    * CCS scores (acp_ccs_smoothed)"
    echo "    * Staleness violations (acp_staleness_violations)"
    echo "    * Conflict resolution (acp_conflicts_detected/resolved)"
    echo "    * Reconciliation runs (acp_reconciliation_runs)"
fi
echo "========================================"
