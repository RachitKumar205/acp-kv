#!/bin/bash
# run-suite.sh - Run full benchmark suite (all 4 scenarios)
# Usage: ./run-suite.sh [output-base-dir]

set -e

OUTPUT_BASE=${1:-results/suite-$(date +%Y%m%d-%H%M%S)}
COOLDOWN=300 # 5 minutes between experiments

echo "=========================================="
echo "ACP Full Benchmark Suite"
echo "=========================================="
echo "output directory: $OUTPUT_BASE"
echo "cooldown between experiments: ${COOLDOWN}s"
echo "=========================================="

mkdir -p "$OUTPUT_BASE"

# save suite metadata
cat > "$OUTPUT_BASE/suite-info.txt" <<EOF
ACP Full Benchmark Suite
Start Time: $(date)
Cooldown: ${COOLDOWN}s

Scenarios:
1. Baseline (120s) - Healthy cluster performance
2. Degraded Latency (180s) - Adaptive quorum under latency stress
3. Network Partition (240s) - Partition tolerance and reconciliation
4. High Load (120s) - Saturation testing vs etcd

Total estimated time: ~30 minutes
EOF

cat "$OUTPUT_BASE/suite-info.txt"

# scenario 1: baseline
echo ""
echo "=========================================="
echo "Running Scenario 1/4: Baseline"
echo "=========================================="
./run-experiment.sh \
    --scenario=baseline \
    --duration=120 \
    --output="$OUTPUT_BASE/1-baseline"

echo ""
echo "cooldown for ${COOLDOWN}s..."
sleep $COOLDOWN

# scenario 2: degraded latency
echo ""
echo "=========================================="
echo "Running Scenario 2/4: Degraded Latency"
echo "=========================================="
./run-experiment.sh \
    --scenario=degraded-latency \
    --duration=180 \
    --output="$OUTPUT_BASE/2-degraded-latency"

echo ""
echo "cooldown for ${COOLDOWN}s..."
sleep $COOLDOWN

# scenario 3: network partition
echo ""
echo "=========================================="
echo "Running Scenario 3/4: Network Partition"
echo "=========================================="
./run-experiment.sh \
    --scenario=partition \
    --duration=240 \
    --output="$OUTPUT_BASE/3-partition"

echo ""
echo "cooldown for ${COOLDOWN}s..."
sleep $COOLDOWN

# scenario 4: high load
echo ""
echo "=========================================="
echo "Running Scenario 4/4: High Load"
echo "=========================================="
./run-experiment.sh \
    --scenario=high-load \
    --duration=120 \
    --output="$OUTPUT_BASE/4-high-load"

# generate consolidated report
echo ""
echo "=========================================="
echo "Generating Suite Report"
echo "=========================================="

cat > "$OUTPUT_BASE/RESULTS.md" <<EOF
# ACP Benchmark Suite Results

**Run Date:** $(date)
**Output Directory:** $OUTPUT_BASE

## Experiments

### 1. Baseline (120s)
- **Goal:** Measure healthy cluster performance
- **Results:** See \`1-baseline/\`
- **Key Metrics:**
  - Throughput: [TODO: extract from logs]
  - Latency p95: [TODO: extract from logs]
  - CCS: Stable >0.75

### 2. Degraded Latency (180s)
- **Goal:** Validate adaptive quorum under latency stress
- **Phases:** 60s normal → 60s degraded (150ms+20ms) → 60s recovery
- **Results:** See \`2-degraded-latency/\`
- **Key Metrics:**
  - CCS drop timing: [TODO: extract]
  - W adjustment timing: [TODO: extract]
  - Latency improvement: [TODO: calculate vs static]

### 3. Network Partition (240s)
- **Goal:** Test partition tolerance and reconciliation
- **Phases:** 60s normal → 60s partition → 120s healing
- **Results:** See \`3-partition/\`
- **Key Metrics:**
  - Availability during partition: [TODO: extract]
  - Reconciliation time: [TODO: extract]
  - Conflicts resolved: [TODO: extract]

### 4. High Load (120s)
- **Goal:** Compare saturation point with etcd
- **Load:** Ramp 100→10k ops/sec
- **Results:** See \`4-high-load/\`
- **Key Metrics:**
  - Saturation point: [TODO: extract]
  - P99 latency curve: [TODO: plot]
  - Throughput comparison: [TODO: vs etcd]

## Summary

**Thesis Claims Validation:**
- [ ] ACP sits between Redis (AP) and etcd (CP) - [evidence]
- [ ] 20-30% adaptive improvement over static - [evidence]
- [ ] Bounded staleness enforced - [evidence]
- [ ] Conflict resolution correctness - [evidence]

**Next Steps:**
1. Run analysis scripts to extract quantitative results
2. Generate plots for thesis
3. Compare with Redis and etcd baselines
4. Statistical significance testing

EOF

echo ""
echo "=========================================="
echo "Suite Complete!"
echo "=========================================="
echo "results: $OUTPUT_BASE"
echo "report: $OUTPUT_BASE/RESULTS.md"
echo ""
echo "next steps:"
echo "1. review individual experiment logs"
echo "2. run analysis scripts to generate plots"
echo "3. compare with redis/etcd baselines"
echo "=========================================="
