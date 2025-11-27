# ACP Binding for go-ycsb

Custom YCSB database binding for benchmarking the Adaptive Control Plane (ACP) key-value store.

## Overview

This binding enables industry-standard YCSB benchmarks to run against ACP clusters, allowing fair performance comparisons with Redis, etcd, and other key-value stores using identical workloads.

## Implementation

**Location:** `go-ycsb-src/db/acp/db.go`

**Features:**
- gRPC client connections to ACP cluster nodes
- Round-robin load balancing across endpoints
- JSON encoding for YCSB's column-family model
- Automatic connection pooling and failover
- Thread-safe operation execution

**Supported Operations:**
- `Read`: Maps to ACP `Get` RPC
- `Insert`: Maps to ACP `Put` RPC
- `Update`: Maps to ACP `Put` RPC (no distinction in ACP)
- `Scan`: Not supported (ACP is a pure key-value store)
- `Delete`: Not supported (not implemented in ACP)

## Building

The binding is automatically included when building the main go-ycsb binary:

```bash
cd go-ycsb-src
make

# Binary output: ../bin/go-ycsb-acp
```

## Configuration

**Required Parameters:**
- `acp.endpoints`: Comma-separated list of ACP node addresses

**Optional Parameters:**
- `threadcount`: Number of concurrent client threads (default: 1)
- `operationcount`: Total operations to execute (default: 1000)
- `recordcount`: Number of records to load (default: 1000)

## Usage

### Basic Benchmark

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

### Multi-Node Load Balancing

Connect to all cluster nodes for distributed load:

```bash
./bin/go-ycsb-acp run acp \
    -P benchmark/workloads/workloada.properties \
    -p acp.endpoints="localhost:8080,localhost:8081,localhost:8082" \
    -p threadcount=10
```

The binding automatically distributes requests across nodes using round-robin selection.

### Port-Forwarding for Local Testing

```bash
# Forward each node to a unique local port
kubectl port-forward acp-node-0 8080:8080 &
kubectl port-forward acp-node-1 8081:8080 &
kubectl port-forward acp-node-2 8082:8080 &

# Run benchmark against all nodes
./bin/go-ycsb-acp run acp \
    -p acp.endpoints="localhost:8080,localhost:8081,localhost:8082"
```

## Output

YCSB reports standard performance metrics:

```
[OVERALL], RunTime(ms), 15234
[OVERALL], Throughput(ops/sec), 1543.2
[READ], Operations, 50000
[READ], AverageLatency(us), 6234
[READ], MinLatency(us), 1230
[READ], MaxLatency(us), 123456
[READ], 95thPercentileLatency(us), 8901
[READ], 99thPercentileLatency(us), 12345
[UPDATE], Operations, 50000
[UPDATE], AverageLatency(us), 7456
[UPDATE], 99thPercentileLatency(us), 15678
```

## Key Encoding

YCSB uses a table/key model, while ACP uses flat keys. The binding creates composite keys:

```
Table: "usertable"
Key: "user1234567890"
ACP Key: "usertable:user1234567890"
```

## Value Encoding

YCSB operations include multiple fields per record. The binding serializes these as JSON:

```json
{
  "field0": "value0_data...",
  "field1": "value1_data...",
  "field9": "value9_data..."
}
```

This JSON object is stored as the value in ACP's key-value store.

## Error Handling

The binding returns errors for:
- Connection failures to any endpoint
- RPC timeouts or failures
- Unsupported operations (Scan, Delete)
- JSON encoding/decoding failures

All errors are propagated to YCSB for reporting in the final statistics.

## Dependencies

**Go Modules:**
```
github.com/pingcap/go-ycsb/pkg/ycsb
google.golang.org/grpc v1.77.0
github.com/rachitkumar205/acp-kv/api/proto
```

## Troubleshooting

**Connection Refused:**
```bash
# Verify ACP is accessible
curl http://localhost:8080/health

# Check port-forward is running
ps aux | grep kubectl.*port-forward
```

**RPC Errors:**
```bash
# Check ACP logs
kubectl logs acp-node-0

# Verify gRPC service is listening
grpcurl -plaintext localhost:8080 list
```

**Performance Issues:**
- Increase `threadcount` for higher concurrency
- Distribute load across all nodes using multi-endpoint configuration
- Reduce `operationcount` for faster testing iterations

## Testing

Minimal test to verify binding functionality:

```bash
# Create minimal workload
cat > test.properties <<EOF
recordcount=100
operationcount=200
workload=site.ycsb.workloads.CoreWorkload
readallfields=true
readproportion=0.5
updateproportion=0.5
scanproportion=0
insertproportion=0
requestdistribution=zipfian
EOF

# Run test
./bin/go-ycsb-acp load acp -P test.properties -p acp.endpoints="localhost:8080"
./bin/go-ycsb-acp run acp -P test.properties -p acp.endpoints="localhost:8080"
```

Expected output: `[OVERALL], Throughput(ops/sec)` should show non-zero value.

## Integration with ACP Benchmarks

This binding is used by the orchestration scripts in `benchmark/orchestration/scenarios/`:

- `baseline.sh`: Healthy cluster performance
- `degraded-latency.sh`: Performance during node failures
- `partition.sh`: Behavior during network partitions
- `high-load.sh`: Stress testing under load

All scripts use YCSB workload files from `benchmark/workloads/`.

## Comparison with Custom Benchmark

| Feature | go-ycsb (this binding) | acp-adaptive-bench |
|---------|------------------------|---------------------|
| Purpose | Standard comparison | Adaptive validation |
| Workloads | YCSB A, B, C | Custom mixed |
| Metrics | Latency, throughput | CCS, quorum, staleness |
| Use Case | Baseline performance | Adaptive behavior |

Use both tools for comprehensive evaluation: YCSB for industry-standard comparisons, custom benchmark for ACP-specific adaptive metrics.

## Contributing

When modifying the binding:

1. Maintain compatibility with YCSB interface
2. Follow lowercase comment style in code
3. Test against live ACP cluster before committing
4. Update this README for any API changes
5. Rebuild binary: `cd go-ycsb-src && make`

## License

See main repository LICENSE file.
