#!/bin/bash
# Simple script to run all 4 scenarios and export metrics

set -e

cd /Users/rachitkumar/projects/acp/acp-kv/benchmark/orchestration

for scenario in baseline degraded partition high-load; do
  echo "========================================"
  echo "Running $scenario..."
  echo "========================================"

  # Create output directory with absolute path
  SCENARIO_DIR="/Users/rachitkumar/projects/acp/acp-kv/benchmark/results/comparison/acp/$scenario"
  mkdir -p "$SCENARIO_DIR"

  # Determine which scenario script to run
  case $scenario in
    baseline)
      ./scenarios/baseline.sh 120 workloada "$SCENARIO_DIR"
      ;;
    degraded)
      ./scenarios/degraded-latency.sh 180 workloada "$SCENARIO_DIR"
      ;;
    partition)
      ./scenarios/partition.sh 240 workloada "$SCENARIO_DIR"
      ;;
    high-load)
      ./scenarios/high-load.sh 120 workloada "$SCENARIO_DIR"
      ;;
  esac

  # Export metrics
  echo "Exporting metrics for $scenario..."
  cd ../analysis
  kubectl port-forward acp-node-0 9090:9090 > /dev/null 2>&1 &
  PF_PID=$!
  sleep 3

  go run export-prometheus.go \
    --prometheus-url="http://localhost:9090" \
    --output="/Users/rachitkumar/projects/acp/acp-kv/benchmark/results/comparison/acp/${scenario}.csv" \
    --start="$(date -u -v-5M +%Y-%m-%dT%H:%M:%SZ)" \
    --end="$(date -u +%Y-%m-%dT%H:%M:%SZ)" || true

  kill $PF_PID 2>/dev/null || true
  cd ../orchestration

  echo "$scenario complete!"
  echo ""
  sleep 5
done

echo "All benchmarks complete! Results in:"
ls -lh /Users/rachitkumar/projects/acp/acp-kv/benchmark/results/comparison/acp/*.csv
