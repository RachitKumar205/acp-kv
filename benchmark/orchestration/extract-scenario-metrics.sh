#!/bin/bash
# extract-scenario-metrics.sh - Extract and summarize benchmark metrics
# Usage: ./extract-scenario-metrics.sh <scenario_dir> [output_json]
#
# Parses YCSB metrics from metrics.txt and scenario-specific measurements
# Optionally summarizes Prometheus metrics for ACP
# Outputs structured JSON for easy comparison and plotting

set -e

SCENARIO_DIR=$1
OUTPUT_JSON=${2:-"$SCENARIO_DIR/summary.json"}

if [ -z "$SCENARIO_DIR" ] || [ ! -d "$SCENARIO_DIR" ]; then
    echo "Usage: $0 <scenario_dir> [output_json]"
    echo "Example: $0 results/research-20250128/1a-baseline-workloada"
    exit 1
fi

METRICS_FILE="$SCENARIO_DIR/metrics.txt"
SCENARIO_LOG="$SCENARIO_DIR/scenario.log"
PROMETHEUS_CSV="$SCENARIO_DIR/prometheus-metrics.csv"

if [ ! -f "$METRICS_FILE" ]; then
    echo "ERROR: metrics.txt not found in $SCENARIO_DIR"
    exit 1
fi

echo "Extracting metrics from $SCENARIO_DIR..."

# Helper function to extract YCSB metric value
extract_ycsb_metric() {
    local operation=$1
    local metric_name=$2
    grep "\\[$operation\\]" "$METRICS_FILE" | grep "$metric_name" | awk '{print $3}' | head -1
}

# Extract YCSB overall metrics
OVERALL_THROUGHPUT=$(extract_ycsb_metric "OVERALL" "Throughput")
OVERALL_RUNTIME=$(extract_ycsb_metric "OVERALL" "RunTime")

# Extract READ metrics
READ_OPS=$(extract_ycsb_metric "READ" "Operations")
READ_AVG_LATENCY=$(extract_ycsb_metric "READ" "AverageLatency")
READ_MIN_LATENCY=$(extract_ycsb_metric "READ" "MinLatency")
READ_MAX_LATENCY=$(extract_ycsb_metric "READ" "MaxLatency")
READ_P95_LATENCY=$(extract_ycsb_metric "READ" "95thPercentileLatency")
READ_P99_LATENCY=$(extract_ycsb_metric "READ" "99thPercentileLatency")

# Extract UPDATE metrics
UPDATE_OPS=$(extract_ycsb_metric "UPDATE" "Operations")
UPDATE_AVG_LATENCY=$(extract_ycsb_metric "UPDATE" "AverageLatency")
UPDATE_MIN_LATENCY=$(extract_ycsb_metric "UPDATE" "MinLatency")
UPDATE_MAX_LATENCY=$(extract_ycsb_metric "UPDATE" "MaxLatency")
UPDATE_P95_LATENCY=$(extract_ycsb_metric "UPDATE" "95thPercentileLatency")
UPDATE_P99_LATENCY=$(extract_ycsb_metric "UPDATE" "99thPercentileLatency")

# Extract scenario-specific metrics from metrics.txt
RECOVERY_TIME=$(grep "Recovery Time:" "$METRICS_FILE" 2>/dev/null | awk '{print $3}' | tr -d 's' || echo "null")
RECONCILIATION_TIME=$(grep "Reconciliation Time:" "$METRICS_FILE" 2>/dev/null | awk '{print $3}' | tr -d 's' || echo "null")
MAX_THROUGHPUT=$(grep "Max Sustainable Throughput:" "$METRICS_FILE" 2>/dev/null | sed 's/Max Sustainable Throughput: //' || echo "null")
DROP_OFF_POINT=$(grep "Drop-off Point:" "$METRICS_FILE" 2>/dev/null | sed 's/Drop-off Point: //' || echo "null")
FAILED_OPS=$(grep "Failed Operations:" "$METRICS_FILE" 2>/dev/null | awk '{print $3}' || echo "null")
REJECTED_WRITES=$(grep "Rejected Writes" "$METRICS_FILE" 2>/dev/null | awk '{print $4}' || echo "null")

# Extract system info from scenario.log
SYSTEM=$(grep "^system:" "$SCENARIO_LOG" 2>/dev/null | awk '{print $2}' || echo "unknown")
DURATION=$(grep "^duration:" "$SCENARIO_LOG" 2>/dev/null | awk '{print $2}' | tr -d 's' || echo "unknown")
WORKLOAD=$(grep "^workload:" "$SCENARIO_LOG" 2>/dev/null | awk '{print $2}' || echo "unknown")

# Detect scenario type from directory name
SCENARIO_NAME=$(basename "$SCENARIO_DIR")
if [[ "$SCENARIO_NAME" == *"baseline"* ]]; then
    SCENARIO_TYPE="baseline"
elif [[ "$SCENARIO_NAME" == *"degraded"* ]] || [[ "$SCENARIO_NAME" == *"failure"* ]]; then
    SCENARIO_TYPE="node_failure"
elif [[ "$SCENARIO_NAME" == *"partition"* ]]; then
    SCENARIO_TYPE="network_partition"
elif [[ "$SCENARIO_NAME" == *"high-load"* ]] || [[ "$SCENARIO_NAME" == *"stress"* ]]; then
    SCENARIO_TYPE="high_load"
else
    SCENARIO_TYPE="unknown"
fi

# Build JSON output
cat > "$OUTPUT_JSON" << EOF
{
  "metadata": {
    "scenario": "$SCENARIO_NAME",
    "scenario_type": "$SCENARIO_TYPE",
    "system": "$SYSTEM",
    "duration_seconds": ${DURATION:-null},
    "workload": "$WORKLOAD",
    "extracted_at": "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  },
  "ycsb": {
    "overall": {
      "throughput_ops_sec": ${OVERALL_THROUGHPUT:-null},
      "runtime_ms": ${OVERALL_RUNTIME:-null}
    },
    "read": {
      "operations": ${READ_OPS:-null},
      "avg_latency_us": ${READ_AVG_LATENCY:-null},
      "min_latency_us": ${READ_MIN_LATENCY:-null},
      "max_latency_us": ${READ_MAX_LATENCY:-null},
      "p95_latency_us": ${READ_P95_LATENCY:-null},
      "p99_latency_us": ${READ_P99_LATENCY:-null}
    },
    "update": {
      "operations": ${UPDATE_OPS:-null},
      "avg_latency_us": ${UPDATE_AVG_LATENCY:-null},
      "min_latency_us": ${UPDATE_MIN_LATENCY:-null},
      "max_latency_us": ${UPDATE_MAX_LATENCY:-null},
      "p95_latency_us": ${UPDATE_P95_LATENCY:-null},
      "p99_latency_us": ${UPDATE_P99_LATENCY:-null}
    }
  },
  "scenario_specific": {
    "recovery_time_seconds": ${RECOVERY_TIME:-null},
    "reconciliation_time_seconds": ${RECONCILIATION_TIME:-null},
    "max_sustainable_throughput": ${MAX_THROUGHPUT:-null},
    "drop_off_point": ${DROP_OFF_POINT:-null},
    "failed_operations": ${FAILED_OPS:-null},
    "rejected_writes": ${REJECTED_WRITES:-null}
  }
EOF

# Add Prometheus metrics summary for ACP
if [ -f "$PROMETHEUS_CSV" ] && [ "$SYSTEM" = "acp" ]; then
    echo "  Parsing Prometheus metrics..."

    # Extract key ACP metrics statistics (if available)
    # This is a simplified summary - full time-series is in prometheus-metrics.csv

    QUORUM_R_AVG=$(grep "acp_current_r" "$PROMETHEUS_CSV" 2>/dev/null | awk -F',' '{sum+=$3; count++} END {if(count>0) print sum/count; else print "null"}')
    QUORUM_W_AVG=$(grep "acp_current_w" "$PROMETHEUS_CSV" 2>/dev/null | awk -F',' '{sum+=$3; count++} END {if(count>0) print sum/count; else print "null"}')
    CCS_AVG=$(grep "acp_ccs_smoothed" "$PROMETHEUS_CSV" 2>/dev/null | awk -F',' '{sum+=$3; count++} END {if(count>0) print sum/count; else print "null"}')
    STALENESS_VIOLATIONS=$(grep "acp_staleness_violations" "$PROMETHEUS_CSV" 2>/dev/null | awk -F',' '{sum+=$3} END {print sum}')
    CONFLICTS_DETECTED=$(grep "acp_conflicts_detected" "$PROMETHEUS_CSV" 2>/dev/null | tail -1 | awk -F',' '{print $3}')
    CONFLICTS_RESOLVED=$(grep "acp_conflicts_resolved" "$PROMETHEUS_CSV" 2>/dev/null | tail -1 | awk -F',' '{print $3}')
    RECONCILIATION_RUNS=$(grep "acp_reconciliation_runs" "$PROMETHEUS_CSV" 2>/dev/null | tail -1 | awk -F',' '{print $3}')

    cat >> "$OUTPUT_JSON" << EOF
,
  "prometheus": {
    "note": "ACP-specific metrics collected during test (see prometheus-metrics.csv for full time-series)",
    "quorum_r_avg": ${QUORUM_R_AVG:-null},
    "quorum_w_avg": ${QUORUM_W_AVG:-null},
    "ccs_avg": ${CCS_AVG:-null},
    "staleness_violations_total": ${STALENESS_VIOLATIONS:-0},
    "conflicts_detected_total": ${CONFLICTS_DETECTED:-null},
    "conflicts_resolved_total": ${CONFLICTS_RESOLVED:-null},
    "reconciliation_runs_total": ${RECONCILIATION_RUNS:-null}
  }
EOF
fi

# Close JSON
cat >> "$OUTPUT_JSON" << EOF
}
EOF

echo "Metrics extracted to $OUTPUT_JSON"

# Pretty print summary to console
echo ""
echo "=== Metrics Summary ==="
echo "Scenario: $SCENARIO_NAME ($SCENARIO_TYPE)"
echo "System: $SYSTEM | Workload: $WORKLOAD | Duration: ${DURATION}s"
echo ""
echo "YCSB Performance:"
echo "  Throughput: ${OVERALL_THROUGHPUT:-N/A} ops/sec"
echo "  Read Ops: ${READ_OPS:-N/A} | Avg Latency: ${READ_AVG_LATENCY:-N/A} μs (P99: ${READ_P99_LATENCY:-N/A} μs)"
echo "  Update Ops: ${UPDATE_OPS:-N/A} | Avg Latency: ${UPDATE_AVG_LATENCY:-N/A} μs (P99: ${UPDATE_P99_LATENCY:-N/A} μs)"

if [ "$SCENARIO_TYPE" = "node_failure" ] && [ "$RECOVERY_TIME" != "null" ]; then
    echo ""
    echo "Node Failure Metrics:"
    echo "  Recovery Time: ${RECOVERY_TIME}s"
    echo "  Failed Operations: ${FAILED_OPS:-N/A}"
fi

if [ "$SCENARIO_TYPE" = "network_partition" ] && [ "$RECONCILIATION_TIME" != "null" ]; then
    echo ""
    echo "Network Partition Metrics:"
    echo "  Reconciliation Time: ${RECONCILIATION_TIME}s"
    echo "  Failed Operations: ${FAILED_OPS:-N/A}"
    echo "  Rejected Writes: ${REJECTED_WRITES:-N/A}"
fi

if [ "$SCENARIO_TYPE" = "high_load" ]; then
    echo ""
    echo "High Load Metrics:"
    echo "  Max Throughput: ${MAX_THROUGHPUT:-N/A}"
    echo "  Drop-off Point: ${DROP_OFF_POINT:-N/A}"
fi

if [ "$SYSTEM" = "acp" ] && [ -f "$PROMETHEUS_CSV" ]; then
    echo ""
    echo "ACP Adaptive Metrics:"
    echo "  Avg Quorum R: ${QUORUM_R_AVG:-N/A} | Avg Quorum W: ${QUORUM_W_AVG:-N/A}"
    echo "  Avg CCS: ${CCS_AVG:-N/A}"
    echo "  Staleness Violations: ${STALENESS_VIOLATIONS:-0}"
    echo "  Conflicts: ${CONFLICTS_DETECTED:-N/A} detected / ${CONFLICTS_RESOLVED:-N/A} resolved"
    echo "  Reconciliation Runs: ${RECONCILIATION_RUNS:-N/A}"
fi

echo "======================="
