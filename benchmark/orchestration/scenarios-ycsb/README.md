# YCSB-Based Comparison Scenarios

System-agnostic YCSB benchmark scenarios for fair comparison between ACP, Redis, and etcd.

## Available Scenarios

### baseline.sh
**Usage**: `./baseline.sh <system> <duration> <workload> <output_dir>`

Measures baseline performance under ideal conditions.
- All nodes healthy
- Minimal latency (1ms)
- Standard YCSB workload

**Example**:
```bash
./baseline.sh acp 120 workloada results/acp-baseline
./baseline.sh redis 120 workloada results/redis-baseline
./baseline.sh etcd 120 workloada results/etcd-baseline
```

### degraded.sh
**Usage**: `./degraded.sh <system> <duration> <workload> <output_dir>`

Tests performance when one node fails mid-benchmark.
- Phase 1 (0-60s): Normal operation
- Phase 2 (60-120s): One node killed
- Phase 3 (120-180s): Recovery period

**Example**:
```bash
./degraded.sh acp 180 workloada results/acp-degraded
./degraded.sh redis 180 workloada results/redis-degraded
./degraded.sh etcd 180 workloada results/etcd-degraded
```

### partition.sh
**Usage**: `./partition.sh <system> <duration> <workload> <output_dir>`

Tests split-brain tolerance and reconciliation.
- Phase 1 (0-60s): Normal operation
- Phase 2 (60-180s): Network partition (node-2 isolated)
- Phase 3 (180-240s): Partition healed, reconciliation

**Example**:
```bash
./partition.sh acp 240 workloada results/acp-partition
./partition.sh redis 240 workloada results/redis-partition
./partition.sh etcd 240 workloada results/etcd-partition
```

### high-load.sh
**Usage**: `./high-load.sh <system> <duration> <workload> <output_dir>`

Tests throughput ceiling with gradually increasing load.
- Phase 1 (0-40s): Low load (100 ops/sec)
- Phase 2 (40-80s): Medium load (1000 ops/sec)
- Phase 3 (80-120s): High load (10000 ops/sec)

**Example**:
```bash
./high-load.sh acp 120 workloada results/acp-highload
./high-load.sh redis 120 workloada results/redis-highload
./high-load.sh etcd 120 workloada results/etcd-highload
```

## Parameters

- **system**: `acp`, `redis`, or `etcd`
- **duration**: Benchmark duration in seconds (default: 120)
- **workload**: YCSB workload file from `benchmark/workloads/` (default: workloada)
- **output_dir**: Directory for results (default: results/baseline)

## Supported Systems

### ACP
- Endpoints: `localhost:8080,localhost:8081,localhost:8082` (via port-forward)
- DB name: `acp`
- Parameter: `acp.endpoints`

### Redis
- Endpoints: `redis-bench-0.redis-headless:6379,redis-bench-1.redis-headless:6379,redis-bench-2.redis-headless:6379`
- DB name: `redis`
- Parameter: `redis.addr`
- Mode: cluster

### etcd
- Endpoints: `etcd-bench-0.etcd-headless:2379,etcd-bench-1.etcd-headless:2379,etcd-bench-2.etcd-headless:2379`
- DB name: `etcd`
- Parameter: `etcd.endpoints`

## Output Files

Each scenario generates:
- `scenario.log` - High-level scenario execution log
- `load.log` - YCSB data loading phase output
- `run.log` - Full YCSB benchmark output
- `metrics.txt` - Extracted performance metrics (throughput, latency, etc.)

## Metrics Collected

From YCSB output:
- **Throughput**: Operations per second
- **Latency**: Average, 95th percentile, 99th percentile (microseconds)
- **Per-operation breakdown**: READ, UPDATE, INSERT statistics
