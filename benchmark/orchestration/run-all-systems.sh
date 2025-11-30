#!/bin/bash
# run-all-systems.sh - Run research suite across all systems (ACP, Redis, etcd)
# Usage: ./run-all-systems.sh [trial_prefix]
#
# Runs the complete research suite for each system sequentially
# Results will be organized as:
#   results/<trial_prefix>-acp/
#   results/<trial_prefix>-redis/
#   results/<trial_prefix>-etcd/

set -e

TRIAL_PREFIX=${1:-comparison-$(date +%Y%m%d-%H%M%S)}

# Get script directory
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

echo "========================================"
echo "Multi-System Research Suite"
echo "========================================"
echo "Running benchmarks for: ACP, Redis, etcd"
echo "Trial prefix: $TRIAL_PREFIX"
echo "Start time: $(date)"
echo "========================================"
echo ""

SUITE_START=$(date +%s)

# Array of systems to test
SYSTEMS=("acp" "redis" "etcd")

for system in "${SYSTEMS[@]}"; do
    echo ""
    echo "========================================"
    echo "Running suite for: $system"
    echo "========================================"

    SYSTEM_START=$(date +%s)

    # Check if system is deployed
    case $system in
        acp)
            POD_COUNT=$(kubectl get pods -l app=acp-node --no-headers 2>/dev/null | grep "Running" | wc -l || echo "0")
            EXPECTED=3
            ;;
        redis)
            POD_COUNT=$(kubectl get pods -l app=redis-bench --no-headers 2>/dev/null | grep "Running" | wc -l || echo "0")
            EXPECTED=3
            ;;
        etcd)
            POD_COUNT=$(kubectl get pods -l app=etcd-bench --no-headers 2>/dev/null | grep "Running" | wc -l || echo "0")
            EXPECTED=3
            ;;
    esac

    if [ "$POD_COUNT" -lt "$EXPECTED" ]; then
        echo "WARNING: $system cluster not fully ready ($POD_COUNT/$EXPECTED pods running)"
        echo "Skipping $system tests..."
        continue
    fi

    # Run the research suite for this system
    "$SCRIPT_DIR/run-research-suite.sh" "$system" "${TRIAL_PREFIX}-${system}" || {
        echo "ERROR: $system suite failed"
        exit 1
    }

    SYSTEM_END=$(date +%s)
    SYSTEM_DURATION=$((SYSTEM_END - SYSTEM_START))
    SYSTEM_DURATION_MIN=$((SYSTEM_DURATION / 60))

    echo ""
    echo "$system suite completed in ${SYSTEM_DURATION}s (~${SYSTEM_DURATION_MIN} min)"
    echo "========================================"

    # Add cooldown between systems (5 minutes)
    if [ "$system" != "etcd" ]; then
        echo ""
        echo "Cooldown: 300s (system transition)"
        echo -n "Waiting: "
        for i in $(seq 1 300); do
            echo -n "."
            sleep 1
            if [ $((i % 60)) -eq 0 ]; then
                echo -n " $((i/60))min "
            fi
        done
        echo " done"
        echo ""
    fi
done

SUITE_END=$(date +%s)
SUITE_DURATION=$((SUITE_END - SUITE_START))
SUITE_DURATION_MIN=$((SUITE_DURATION / 60))
SUITE_DURATION_HR=$((SUITE_DURATION / 3600))

echo ""
echo "========================================"
echo "Multi-System Suite Complete!"
echo "========================================"
echo "Total Duration: ${SUITE_DURATION}s (~${SUITE_DURATION_MIN} min / ~${SUITE_DURATION_HR}h)"
echo "End time: $(date)"
echo ""
echo "Results Summary:"
echo "  ACP:   results/${TRIAL_PREFIX}-acp/"
echo "  Redis: results/${TRIAL_PREFIX}-redis/"
echo "  etcd:  results/${TRIAL_PREFIX}-etcd/"
echo ""
echo "Next Steps:"
echo "  1. Extract metrics from each system:"
echo "     for dir in results/${TRIAL_PREFIX}-*/*/; do"
echo "       ./extract-scenario-metrics.sh \"\$dir\""
echo "     done"
echo ""
echo "  2. Compare results across systems by scenario:"
echo "     - Baseline: compare throughput/latency (workload A & B)"
echo "     - Node Failure: compare recovery time"
echo "     - Network Partition: compare reconciliation behavior"
echo "     - High Load: compare max sustainable throughput"
echo ""
echo "  3. For ACP, analyze Prometheus metrics in prometheus-metrics.csv"
echo "     to show adaptive quorum adjustments"
echo "========================================"
