#!/bin/bash
# collect-metrics.sh - Query Prometheus for ACP metrics during benchmark
# Usage: ./collect-metrics.sh <start_time> <end_time> <output_file> [prometheus_url]
#
# Collects time-series data for:
# - Quorum sizes (current_r, current_w)
# - CCS scores (ccs_smoothed)
# - Staleness violations
# - Conflicts detected/resolved
# - Reconciliation runs

set -e

START_TIME=$1
END_TIME=$2
OUTPUT_FILE=$3
PROMETHEUS_URL=${4:-http://localhost:9090}

if [ -z "$START_TIME" ] || [ -z "$END_TIME" ] || [ -z "$OUTPUT_FILE" ]; then
    echo "Usage: $0 <start_time> <end_time> <output_file> [prometheus_url]"
    echo "Example: $0 1640000000 1640000120 metrics.csv"
    exit 1
fi

echo "Collecting Prometheus metrics from $START_TIME to $END_TIME..."
echo "Output: $OUTPUT_FILE"

# Create output directory if needed
mkdir -p "$(dirname "$OUTPUT_FILE")"

# Initialize output file with header
cat > "$OUTPUT_FILE" << 'EOF'
# ACP Prometheus Metrics Export
# Format: timestamp,metric_name,value,labels
EOF

# Helper function to query Prometheus and format output
query_metric() {
    local metric=$1
    local query=$2

    # Query Prometheus range query API
    RESPONSE=$(curl -sG "${PROMETHEUS_URL}/api/v1/query_range" \
        --data-urlencode "query=${query}" \
        --data-urlencode "start=${START_TIME}" \
        --data-urlencode "end=${END_TIME}" \
        --data-urlencode "step=5s")

    # Check if we have jq for proper JSON parsing
    if command -v jq > /dev/null 2>&1; then
        # Use jq for robust JSON parsing
        echo "$RESPONSE" | jq -r '.data.result[] | .metric as $labels | .values[] | "\(.[0]),'"$metric"',\(.[1]),\($labels | to_entries | map("\(.key)=\(.value)") | join(";"))"' >> "$OUTPUT_FILE" 2>/dev/null || true
    elif command -v python3 > /dev/null 2>&1; then
        # Fallback to Python for JSON parsing
        echo "$RESPONSE" | python3 -c "
import json, sys
try:
    data = json.load(sys.stdin)
    for result in data.get('data', {}).get('result', []):
        labels = ';'.join([f'{k}={v}' for k, v in result.get('metric', {}).items()])
        for value in result.get('values', []):
            print(f'{value[0]},${metric},{value[1]},{labels}')
except: pass
" >> "$OUTPUT_FILE" 2>/dev/null || true
    else
        # Simple fallback using grep/sed (may be less reliable)
        echo "$RESPONSE" | grep -o '\[\[.*\]\]' | \
            sed 's/^\[\[//;s/\]\]$//' | \
            tr '],[' '\n' | \
            while IFS= read -r point; do
                if [ -n "$point" ]; then
                    TS=$(echo "$point" | cut -d',' -f1)
                    VAL=$(echo "$point" | cut -d',' -f2 | tr -d '"')
                    if [ -n "$TS" ] && [ -n "$VAL" ]; then
                        echo "${TS},${metric},${VAL}," >> "$OUTPUT_FILE"
                    fi
                fi
            done
    fi
}

echo "Querying ACP metrics..."

# Query key ACP metrics
if command -v curl > /dev/null 2>&1; then
    # Test Prometheus connectivity
    if ! curl -s "${PROMETHEUS_URL}/api/v1/query?query=up" > /dev/null 2>&1; then
        echo "Warning: Cannot connect to Prometheus at ${PROMETHEUS_URL}"
        echo "Creating empty metrics file..."
        echo "# No metrics collected - Prometheus not accessible" >> "$OUTPUT_FILE"
        exit 0
    fi

    echo "  - acp_current_r (read quorum)"
    query_metric "acp_current_r" "acp_current_r" 2>/dev/null || true

    echo "  - acp_current_w (write quorum)"
    query_metric "acp_current_w" "acp_current_w" 2>/dev/null || true

    echo "  - acp_ccs_smoothed (consistency score)"
    query_metric "acp_ccs_smoothed" "acp_ccs_smoothed" 2>/dev/null || true

    echo "  - acp_staleness_violations_total"
    query_metric "acp_staleness_violations" "rate(acp_staleness_violations_total[30s])" 2>/dev/null || true

    echo "  - acp_conflicts_detected_total"
    query_metric "acp_conflicts_detected" "acp_conflicts_detected_total" 2>/dev/null || true

    echo "  - acp_conflicts_resolved_total"
    query_metric "acp_conflicts_resolved" "acp_conflicts_resolved_total" 2>/dev/null || true

    echo "  - acp_reconciliation_runs_total"
    query_metric "acp_reconciliation_runs" "acp_reconciliation_runs_total" 2>/dev/null || true

    echo "  - acp_write_ops_total (throughput)"
    query_metric "acp_write_ops" "rate(acp_write_ops_total[30s])" 2>/dev/null || true

    echo "  - acp_read_ops_total (throughput)"
    query_metric "acp_read_ops" "rate(acp_read_ops_total[30s])" 2>/dev/null || true

    echo "Metrics collection complete: $OUTPUT_FILE"
else
    echo "Warning: curl not found, skipping Prometheus collection"
    echo "# curl not available" >> "$OUTPUT_FILE"
fi

# Count collected metrics
METRIC_COUNT=$(grep -v "^#" "$OUTPUT_FILE" | wc -l)
echo "Collected $METRIC_COUNT data points"
