# ACP Benchmark Package

Custom benchmark framework made for ACP.

## Overview

This basic benchmark package was created to validate ACP and the benefits we claim for it

## Quick Start

```bash
# Run all benchmarks
cd benchmark/orchestration
./run-comparison.sh --scenarios baseline,degraded,partition,high-load --systems acp
```

## Dir Structure

```
benchmark/
├── adaptive/             # ACP adaptive bench
├── orchestration/        # Experiment automation
│   ├── scenarios/        # Baseline, degraded, partition, high-load
│   └── run-*.sh          # Orchestration scripts
├── workloads/            # YCSB workload configurations
├── network/              # Chaos injection scripts
│   ├── inject-latency.sh
│   └── restore-network.sh
└── compare/              # Redis and etcd deployments
```

## Benchmark Tools

### Yahoo Cloud Serving Benchmark

Custom YCSB binding for ACP enables fair performance comparisons with Redis, etcd, and other key-value stores using identical workloads.

**Implementation:** `go-ycsb-vendor/db/acp/db.go`

**What ACP for YCSB does:**
- gRPC client connections to ACP cluster nodes
- Round-robin load balancing across endpoints
- JSON encoding for YCSB's column-family model
- Automatic connection pooling

**Supported Operations:**
- `Read`: Maps to ACP `Get` RPC
- `Insert`/`Update`: Map to ACP `Put` RPC
- `Scan`: Not supported (ACP is a pure key-value store)
- `Delete`: Not yet supported, WIP

**Building:**
```bash
cd go-ycsb-vendor
# Build instructions in the Makefile are outdated, and I cba to fix them yet, so instead we have to manually build with this command
go build -o ../bin/go-ycsb-acp ./cmd/go-ycsb
# Binary output: ../bin/go-ycsb-vendor
```

**Configuration:**

Required parameters:
- `acp.endpoints`: Comma-separated list of ACP node addresses

Optional parameters:
- `threadcount`: Number of concurrent client threads (default: 1)
- `operationcount`: Total operations to execute (default: 1000)
- `recordcount`: Number of records to load (default: 1000)

**Usage:**

Basic benchmark:
```bash
# Load 10,000 records
./bin/go-ycsb-acp load acp \
    -P benchmark/workloads/workloada.properties \
    -p acp.endpoints="localhost:8080" \
    -p recordcount=10000

# Run 100,000 operations
./bin/go-ycsb-acp run acp \
    -P benchmark/workloads/workloada.properties \
    -p acp.endpoints="localhost:8080" \
    -p operationcount=100000 \
    -p threadcount=10
```

Multi-node load balancing:
```bash
# Connect to all cluster nodes
./bin/go-ycsb-acp run acp \
    -P benchmark/workloads/workloada.properties \
    -p acp.endpoints="localhost:8080,localhost:8081,localhost:8082" \
    -p threadcount=10
```

Port-forwarding for local testing:
```bash
# Forward each node to a unique local port
kubectl port-forward acp-node-0 8080:8080 &
kubectl port-forward acp-node-1 8081:8080 &
kubectl port-forward acp-node-2 8082:8080 &

# Run benchmark against all nodes
./bin/go-ycsb-acp run acp \
    -p acp.endpoints="localhost:8080,localhost:8081,localhost:8082"
```

**Workloads:**
- Workload A: 50% read / 50% update (general-purpose)
- Workload B: 95% read / 5% update (read-heavy)
- Workload C: 100% read (read-only)

All workloads use Zipfian distribution to model realistic hotspot patterns.

**Output Metrics:**
```
[OVERALL], RunTime(ms), 15234
[OVERALL], Throughput(ops/sec), 1543.2
[READ], Operations, 50000
[READ], AverageLatency(us), 6234
[READ], 95thPercentileLatency(us), 8901
[READ], 99thPercentileLatency(us), 12345
[UPDATE], Operations, 50000
[UPDATE], AverageLatency(us), 7456
[UPDATE], 99thPercentileLatency(us), 15678
```

**Key/Value Encoding:**

YCSB uses a table/key model; ACP uses flat keys. The binding creates composite keys:
```
Table: "usertable"
Key: "user1234567890"
ACP Key: "usertable:user1234567890"
```

YCSB operations include multiple fields per record, serialized as JSON:
```json
{
  "field0": "value0_data...",
  "field1": "value1_data...",
  "field9": "value9_data..."
}
```

**Troubleshooting:**

Connection refused:
```bash
# Verify ACP is accessible
curl http://localhost:8080/health

# Check port-forward is running
ps aux | grep kubectl.*port-forward
```

RPC errors:
```bash
# Check ACP logs
kubectl logs acp-node-0

# Verify gRPC service is listening
grpcurl -plaintext localhost:8080 list
```

Performance issues:
- Increase `threadcount` for higher concurrency
- Distribute load across all nodes using multi-endpoint configuration
- Reduce `operationcount` for faster testing iterations

### Custom ACP Benchmark

ACP-specific tool for validating adaptive behavior (`cmd/acp-adaptive-bench`).

**Implementation:**
- `benchmark/adaptive/client_pool.go` - gRPC connection pooling with round-robin load balancing (119 lines)
- `benchmark/adaptive/metrics.go` - Prometheus metrics collector with HTTP API client (250 lines)
- `cmd/acp-adaptive-bench/main.go` - CLI benchmark tool (390+ lines)

**What the ACP Benchmark does:**
- Real-time CCS monitoring via Prometheus API
- Quorum adjustment tracking (R, W values over time)
- Staleness violation counting
- Client-side latency measurement
- CSV export of metric snapshots
- Concurrent worker pool with rate limiting

**Building:**
```bash
cd cmd/acp-adaptive-bench
go build
# Binary output: ./acp-adaptive-bench (16MB)
```

**Usage:**
```bash
# Basic benchmark with CCS monitoring
./acp-adaptive-bench \
    --endpoints="localhost:8080" \
    --prometheus-url="http://localhost:9090" \
    --mode=ccs-watch \
    --duration=180s \
    --concurrency=10 \
    --workload=mixed \
    --target-throughput=1000 \
    --output=results.csv

# Modes:
#   ccs-watch   - Monitor CCS and trigger operations (default)
#   continuous  - Constant load without CCS monitoring
#   burst       - Burst traffic patterns

# Workloads:
#   mixed       - 50% read / 50% write (default)
#   read-heavy  - 95% read / 5% write
#   write-heavy - 5% read / 95% write
```

**Output Metrics:**
- Throughput (ops/sec)
- Success/failure counts
- Latency statistics (avg, min, max)
- CCS (raw and smoothed)
- Current quorum values (R, W)
- Quorum adjustments count
- Staleness violations

**Note:** Use YCSB for baseline comparisons/comparisons with other tools, ACP bench for validating novel ACP features.

## Experiments

### Baseline (120s)
Healthy cluster performance characterization.
- Target: 1000 ops/sec
- Workload: Mixed (50/50 read/write)
- Validates: Competitive baseline performance

### Degraded (180s)
Adaptive quorum validation under node failure.
- Phase 1 (0-60s): Normal operation
- Phase 2 (60-120s): Node killed
- Phase 3 (120-180s): Recovery
- Validates: CCS-driven adaptation, latency stability

### Partition (240s)
Partition tolerance and reconciliation.
- Tests write availability during split-brain
- Validates bounded staleness guarantee
- Measures conflict resolution latency

### High-Load (120s)
Performance under increasing load.
- Load ramp: 100 to 10,000 ops/sec
- Validates: Throughput ceiling, adaptive behavior under saturation

## Running Experiments

### Quick Start: All Scenarios

Run all 4 scenarios sequentially with default settings:
```bash
cd benchmark/orchestration
./run-all-scenarios.sh
```

Runs: baseline (120s) → degraded (180s) → partition (240s) → high-load (120s)

Exports Prometheus metrics to CSV after each scenario.

### Single Scenario

Run one scenario with custom parameters:
```bash
./run-experiment.sh --scenario=baseline --duration=120 --workload=workloada
```

**Options:**
- `--scenario`: baseline, degraded, partition, or high-load
- `--duration`: Seconds (default: 120)
- `--workload`: workloada, workloadb, or workloadc (default: workloada)
- `--output`: Output directory (default: ../results)
- `--prometheus-url`: Prometheus endpoint (default: http://localhost:9090)

### System Comparison

Compare ACP against Redis/etcd with identical workloads:
```bash
./run-comparison.sh --scenarios=baseline,degraded --systems=acp,redis,etcd
```

**Options:**
- `--scenarios`: Comma-separated list or "all"
- `--systems`: acp, redis, etcd (currently only acp implemented)
- `--output-dir`: Results directory (default: ../results/comparison)
- `--skip-teardown`: Keep cluster running after tests

### Multi-Trial Statistical Validation

Run multiple trials for error bars and confidence intervals:
```bash
./aggregate-trials.sh --scenarios=baseline,degraded --trials=5
```

**Options:**
- `--scenarios`: Comma-separated scenario list
- `--trials`: Number of repetitions (default: 3)
- `--output-dir`: Results directory (default: ../results/trials)
- `--system`: Target system (default: acp)

Generates aggregated CSV with mean, stddev, and 95% confidence intervals.

### Full Suite with Reporting

Run all scenarios with cooldown periods:
```bash
./run-suite.sh
```

**Features:**
- 5-minute cooldown between scenarios
- Health validation before each test
- Saves metadata (timestamps, cluster state)

## Data Collection

Export Prometheus metrics to CSV:
```bash
cd benchmark/analysis
go run export-prometheus.go \
    --start="2025-01-24T10:00:00Z" \
    --end="2025-01-24T11:30:00Z" \
    --output=../results/experiment.csv
```

**Metrics Exported (18 total):**
- CCS (raw, smoothed)
- Quorum values (R, W)
- Latency percentiles (p50, p95, p99)
- Success/failure rates
- Staleness violations
- Conflict resolution events

## Network Chaos Injection

Manual failure injection for custom experiments:
```bash
cd benchmark/network

# Inject 150ms latency
./inject-latency.sh acp-node-0 150 20

# Create partition
./partition-network.sh acp-node-2 acp-node-0

# Restore
./restore-network.sh
```

## Configuration

Key environment variables:
- `PROMETHEUS_URL`: Prometheus endpoint (default: `http://localhost:9090`)
- `BENCHMARK_DURATION`: Experiment duration in seconds
- `BENCHMARK_CONCURRENCY`: Concurrent clients (default: 10)
- `TARGET_THROUGHPUT`: Target ops/sec (default: 1000)

## Prerequisites

**Software:**
- Kubernetes cluster with ACP deployed (3 nodes minimum)
- Go 1.21+ for building custom benchmark tools and Prometheus export
- kubectl configured for cluster access

**Building Custom Benchmark:**
```bash
# Build acp-adaptive-bench tool
cd cmd/acp-adaptive-bench
go build
# Binary created: ./acp-adaptive-bench (16MB)
```

## Troubleshooting

**Missing Prometheus data:**
```bash
kubectl port-forward acp-node-0 9090:9090 &
curl http://localhost:9090/metrics | grep acp_
```

**Empty CSV exports:**
Verify time range matches experiment execution window.

**Figure generation errors:**
Ensure all required CSV files exist in data directory.

## License

See main repository LICENSE file.
