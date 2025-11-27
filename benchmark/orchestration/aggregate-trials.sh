#!/bin/bash
#
# Multi-Trial Aggregation Script
#
# Runs multiple trials of each scenario to compute statistical aggregates (mean, std, CI).
# This ensures reproducibility and provides error bars for publication figures.
#
# Usage:
#   ./aggregate-trials.sh [options]
#
# Options:
#   --scenarios SCENARIO_LIST   Comma-separated scenarios (default: all)
#   --trials N                  Number of trials per scenario (default: 5)
#   --output-dir DIR            Output directory for results
#   --system SYSTEM             System to benchmark (acp, redis, etcd)
#

set -e

# Default configuration
SCENARIOS="baseline,degraded,partition,high-load"
TRIALS=5
OUTPUT_DIR="../results/trials"
SYSTEM="acp"

# Parse arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        --scenarios)
            SCENARIOS="$2"
            shift 2
            ;;
        --trials)
            TRIALS="$2"
            shift 2
            ;;
        --output-dir)
            OUTPUT_DIR="$2"
            shift 2
            ;;
        --system)
            SYSTEM="$2"
            shift 2
            ;;
        *)
            echo "Unknown option: $1"
            exit 1
            ;;
    esac
done

IFS=',' read -r -a SCENARIO_ARRAY <<< "$SCENARIOS"

echo "=========================================="
echo "MULTI-TRIAL BENCHMARK EXECUTION"
echo "=========================================="
echo "System: $SYSTEM"
echo "Scenarios: ${SCENARIO_ARRAY[*]}"
echo "Trials per scenario: $TRIALS"
echo "Output: $OUTPUT_DIR"
echo ""

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# Create output directory
mkdir -p "$OUTPUT_DIR"

# Function to run a single trial
run_trial() {
    local scenario=$1
    local trial_num=$2

    echo "[Trial $trial_num/$TRIALS] Running $scenario..."

    # Create trial output directory
    local trial_dir="$OUTPUT_DIR/trial$trial_num/$SYSTEM"
    mkdir -p "$trial_dir"

    # Deploy cluster if not already running
    if ! kubectl get statefulset acp-node &>/dev/null; then
        echo "  Deploying $SYSTEM cluster..."
        kubectl apply -f "$SCRIPT_DIR/../../k8s/statefulset.yaml"
        kubectl apply -f "$SCRIPT_DIR/../../k8s/service.yaml"
        kubectl wait --for=condition=ready pod -l app=acp-node --timeout=120s
        sleep 10
    fi

    # Run scenario
    case $scenario in
        baseline)
            "$SCRIPT_DIR/scenarios/baseline.sh" > "$trial_dir/baseline.log" 2>&1
            ;;
        degraded)
            "$SCRIPT_DIR/scenarios/degraded-latency.sh" > "$trial_dir/degraded.log" 2>&1
            ;;
        partition)
            "$SCRIPT_DIR/scenarios/partition.sh" > "$trial_dir/partition.log" 2>&1
            ;;
        high-load)
            "$SCRIPT_DIR/scenarios/high-load.sh" > "$trial_dir/high-load.log" 2>&1
            ;;
        *)
            echo "Error: Unknown scenario: $scenario"
            return 1
            ;;
    esac

    # Export metrics
    echo "  Exporting metrics..."
    kubectl port-forward acp-node-0 9090:9090 > /dev/null 2>&1 &
    local pf_pid=$!
    sleep 3

    cd "$SCRIPT_DIR/../analysis"
    go run export-prometheus.go \
        --prometheus-url="http://localhost:9090" \
        --output="$trial_dir/$scenario.csv" \
        --start="$(date -u -d '5 minutes ago' +%Y-%m-%dT%H:%M:%SZ)" \
        --end="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

    kill $pf_pid 2>/dev/null || true

    echo "  Trial $trial_num complete: $trial_dir/$scenario.csv"

    # Restart cluster for next trial (ensure clean state)
    echo "  Restarting cluster for next trial..."
    kubectl delete pod -l app=acp-node
    kubectl wait --for=condition=ready pod -l app=acp-node --timeout=120s
    sleep 10
}

# Run trials for each scenario
START_TIME=$(date +%s)

for scenario in "${SCENARIO_ARRAY[@]}"; do
    echo ""
    echo "=========================================="
    echo "SCENARIO: $scenario"
    echo "=========================================="

    for trial in $(seq 1 $TRIALS); do
        run_trial "$scenario" "$trial"

        # Brief cooldown between trials
        sleep 5
    done

    echo "[$(date +%T)] All trials complete for $scenario"
done

# Aggregate results using Python
echo ""
echo "=========================================="
echo "AGGREGATING TRIAL RESULTS"
echo "=========================================="

cd "$SCRIPT_DIR/../analysis"

python3 << 'EOF'
import pandas as pd
import numpy as np
from pathlib import Path
import sys

output_dir = Path(sys.argv[1])
trials = int(sys.argv[2])
scenarios = sys.argv[3].split(',')

aggregated_dir = output_dir / 'aggregated'
aggregated_dir.mkdir(parents=True, exist_ok=True)

for scenario in scenarios:
    print(f"\nAggregating {scenario}...")

    # Load all trials
    trial_data = []
    for trial_num in range(1, trials + 1):
        csv_path = output_dir / f"trial{trial_num}" / "acp" / f"{scenario}.csv"
        if csv_path.exists():
            df = pd.read_csv(csv_path)
            df['trial'] = trial_num
            trial_data.append(df)
        else:
            print(f"  Warning: Missing {csv_path}")

    if not trial_data:
        print(f"  Error: No trial data found for {scenario}")
        continue

    # Concatenate all trials
    all_data = pd.concat(trial_data, ignore_index=True)

    # Determine time column
    if 'timestamp' in all_data.columns:
        time_col = 'timestamp'
    elif 'time' in all_data.columns:
        time_col = 'time'
    else:
        print(f"  Error: No time column found")
        continue

    # Get metric columns (exclude trial and time)
    metric_cols = [col for col in all_data.columns if col not in [time_col, 'trial']]

    # Aggregate by time
    mean_df = all_data.groupby(time_col)[metric_cols].mean().reset_index()
    std_df = all_data.groupby(time_col)[metric_cols].std().reset_index()

    # Save aggregated data
    mean_csv = aggregated_dir / f"{scenario}_mean.csv"
    std_csv = aggregated_dir / f"{scenario}_std.csv"

    mean_df.to_csv(mean_csv, index=False)
    std_df.to_csv(std_csv, index=False)

    print(f"  Saved: {mean_csv}")
    print(f"  Saved: {std_csv}")

    # Compute summary statistics
    print(f"\n  Summary statistics for {scenario}:")
    for col in metric_cols[:5]:  # Show first 5 metrics
        if col in mean_df.columns:
            mean_val = mean_df[col].mean()
            std_val = std_df[col].mean()
            print(f"    {col}: {mean_val:.4f} ± {std_val:.4f}")

print("\nAggregation complete!")
EOF

python3 - "$OUTPUT_DIR" "$TRIALS" "$SCENARIOS"

END_TIME=$(date +%s)
DURATION=$((END_TIME - START_TIME))

echo ""
echo "=========================================="
echo "MULTI-TRIAL BENCHMARK COMPLETE"
echo "=========================================="
echo "Duration: ${DURATION}s ($(($DURATION / 60)) minutes)"
echo "Total trials: $((${#SCENARIO_ARRAY[@]} * TRIALS))"
echo ""
echo "Results:"
echo "  Raw trials: $OUTPUT_DIR/trial*/"
echo "  Aggregated: $OUTPUT_DIR/aggregated/"
echo ""
echo "Next steps:"
echo "  1. Review trial logs in $OUTPUT_DIR/trial*/acp/"
echo "  2. Check aggregated CSVs in $OUTPUT_DIR/aggregated/"
echo "  3. Generate figures with error bars:"
echo "     cd ../analysis/paper_figures"
echo "     python generate_all.py --data-dir $OUTPUT_DIR/aggregated --output-dir ./figures"
