#!/bin/bash
# run-experiment.sh - Main experiment orchestration script
# Usage: ./run-experiment.sh --scenario=<name> --duration=<seconds> --output=<dir>

set -e

# default parameters
SCENARIO="baseline"
DURATION=120
OUTPUT_DIR="results/experiment-$(date +%Y%m%d-%H%M%S)"
WORKLOAD="workloada"
PROMETHEUS_URL="http://localhost:9090"

# parse command line arguments
for arg in "$@"; do
    case $arg in
        --scenario=*)
            SCENARIO="${arg#*=}"
            ;;
        --duration=*)
            DURATION="${arg#*=}"
            ;;
        --output=*)
            OUTPUT_DIR="${arg#*=}"
            ;;
        --workload=*)
            WORKLOAD="${arg#*=}"
            ;;
        --prometheus-url=*)
            PROMETHEUS_URL="${arg#*=}"
            ;;
        --help)
            echo "usage: $0 [options]"
            echo "options:"
            echo "  --scenario=<name>      scenario to run (baseline, degraded-latency, partition, high-load)"
            echo "  --duration=<seconds>   experiment duration in seconds (default: 120)"
            echo "  --output=<dir>         output directory for results"
            echo "  --workload=<name>      ycsb workload to use (default: workloada)"
            echo "  --prometheus-url=<url> prometheus URL (default: http://localhost:9090)"
            exit 0
            ;;
    esac
done

echo "=========================================="
echo "ACP Benchmark Experiment"
echo "=========================================="
echo "scenario: $SCENARIO"
echo "duration: ${DURATION}s"
echo "workload: $WORKLOAD"
echo "output: $OUTPUT_DIR"
echo "=========================================="

# create output directory
mkdir -p "$OUTPUT_DIR"

# save experiment metadata
cat > "$OUTPUT_DIR/metadata.txt" <<EOF
Experiment: $SCENARIO
Start Time: $(date)
Duration: ${DURATION}s
Workload: $WORKLOAD
Prometheus: $PROMETHEUS_URL
EOF

# step 1: validate cluster health
echo ""
echo "[1/6] validating cluster health..."
if ! kubectl get pods -l app=acp-node | grep -q Running; then
    echo "error: acp cluster not running"
    exit 1
fi
RUNNING_PODS=$(kubectl get pods -l app=acp-node --no-headers | grep Running | wc -l)
echo "  cluster healthy: $RUNNING_PODS pods running"

# step 2: restore network (clean slate)
echo ""
echo "[2/6] restoring network (clean state)..."
../network/restore-network.sh

# step 3: run scenario
echo ""
echo "[3/6] running scenario: $SCENARIO"
SCENARIO_SCRIPT="scenarios/${SCENARIO}.sh"
if [ ! -f "$SCENARIO_SCRIPT" ]; then
    echo "error: scenario script not found: $SCENARIO_SCRIPT"
    exit 1
fi

# run scenario in background
bash "$SCENARIO_SCRIPT" "$DURATION" "$WORKLOAD" "$OUTPUT_DIR" &
SCENARIO_PID=$!

# step 4: collect metrics during experiment
echo ""
echo "[4/6] collecting metrics..."
START_TIME=$(date -u +%s)
END_TIME=$((START_TIME + DURATION + 30)) # extra 30s buffer

# wait for scenario to complete
wait $SCENARIO_PID
SCENARIO_EXIT=$?

if [ $SCENARIO_EXIT -ne 0 ]; then
    echo "error: scenario failed with exit code $SCENARIO_EXIT"
fi

# step 5: cleanup network chaos
echo ""
echo "[5/6] cleaning up network chaos..."
../network/restore-network.sh

# step 6: export prometheus data
echo ""
echo "[6/6] exporting prometheus data..."
# note: this step will be implemented in phase 7 with the prometheus exporter tool

echo ""
echo "=========================================="
echo "experiment complete!"
echo "results saved to: $OUTPUT_DIR"
echo "=========================================="
echo ""
echo "results summary:"
ls -lh "$OUTPUT_DIR"

exit $SCENARIO_EXIT
