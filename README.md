# ACP-Key Value Store

A distributed multi-node key-value store with adaptive quorum-based replication. ACP automatically adjusts read (R) and write (W) quorum parameters in real-time based on cluster health metrics including latency, availability, variance, and error rates.

## Features

- **Adaptive Quorum System**: Dynamic R/W adjustment based on Consistency Confidence Score (CCS)
- **CCS Computation**: Calculating an authoritative score to figure out what to do with our quorum params (RTT, availability, variance, error rates)
- **Moving Average Smoothing**: 10-sample sliding window to prevent oscillation
- **Hysteresis Mechanism**: 5-second lockout period between adjustments
- **Real-time Monitoring**: Prometheus metrics for all adaptive quorum parameters

### Build and Run

#### Docker Compose (Development)

1. **Clone the repo**
   ```bash
   git clone https://github.com/rachitkumar/acp-kv.git
   cd acp-kv
   ```

2. **Generate gRPC code**
   ```bash
   make proto
   ```

3. **Start the cluster**
   ```bash
   make docker-up
   ```

4. **Access services**
   - Node 1 gRPC: `localhost:8080`
   - Node 2 gRPC: `localhost:8081`
   - Node 3 gRPC: `localhost:8082`
   - Prometheus: `http://localhost:9093`
   - Prometheus Connection URL: `http://prometheus:9090`
   - Grafana: `http://localhost:3000` (admin/admin)

#### Kubernetes

1. **Build the Docker image**
   ```bash
   make build
   docker build -t acp-kv:latest .
   ```

2. **Deploy to Kubernetes**
   ```bash
   kubectl apply -f k8s/service.yaml
   kubectl apply -f k8s/headless-service.yaml
   kubectl apply -f k8s/statefulset.yaml
   ```

3. **Verify deployment**
   ```bash
   kubectl get pods -l app=acp-node
   kubectl logs acp-node-0 --tail=50
   ```

4. **Access services**
   ```bash
   # port-forward gRPC endpoint
   kubectl port-forward acp-node-0 8080:8080

   # port-forward Prometheus metrics
   kubectl port-forward acp-node-0 9090:9090
   ```

## Testing

### Unit Tests

```bash
go test ./internal/storage -v
go test ./internal/replication -v
go test ./internal/adaptive -v
```

### Integration Tests

Run integration tests (requires running cluster):
```bash
go test ./test -v
```

### Testing Adaptive Quorum Params

#### 1. Monitor CCS and Quorum Adjustments

```bash
# watch adjuster logs in real-time
kubectl logs -f acp-node-0 | grep -E "(adjuster|ccs|quorum)"

# check prometheus metrics
kubectl port-forward acp-node-0 9090:9090
curl http://localhost:9090/metrics | grep acp_ccs
curl http://localhost:9090/metrics | grep acp_current
curl http://localhost:9090/metrics | grep acp_quorum_adjustments
```

Expected output shows:
- `acp_ccs_raw`: Raw consistency confidence score (0.0-1.0)
- `acp_ccs_smoothed`: 10-sample moving average of CCS
- `acp_ccs_component_rtt`: RTT health component (1.0 = healthy)
- `acp_ccs_component_avail`: Availability health (success rate)
- `acp_ccs_component_var`: Variance health (1.0 = low variance)
- `acp_ccs_component_error`: Error health (1.0 = no errors)
- `acp_current_r`: Current read quorum size
- `acp_current_w`: Current write quorum size
- `acp_quorum_adjustments_total`: Total number of adjustments
- `acp_quorum_adjustment_reason_total{reason="tighten"}`: Tighten adjustments
- `acp_quorum_adjustment_reason_total{reason="relax"}`: Relax adjustments
- `acp_hysteresis_active`: Whether in lockout period (0 or 1)

#### 2. Verify CCS Computation

In healthy cluster conditions (all nodes up, low latency):
```bash
# check CCS is high (close to 1.0)
kubectl logs acp-node-0 --tail=10 | grep smoothed_ccs
```

Expected: `"smoothed_ccs":1.0` or close to 1.0

#### 3. Test Tightening Behavior (High CCS)

When CCS > 0.80 (tighten threshold), system should tighten (increase W, decrease R):
```bash
# watch for tightening
kubectl logs -f acp-node-0 | grep "tighten"
```

Expected logs:
```
"msg":"ccs above tighten threshold","smoothed_ccs":0.82,"threshold":0.8
"msg":"quorum adjusted","old_r":2,"new_r":1,"old_w":2,"new_w":3,"reason":"tighten"
```

#### 4. Test Relaxation Behavior (Low CCS)

To test relaxation when CCS < 0.75 (relax threshold), introduce network latency:
```bash
# inject 300ms latency on pod network interface
kubectl exec -it acp-node-1 -- tc qdisc add dev eth0 root netem delay 300ms

# watch for relaxation
kubectl logs -f acp-node-0 | grep "relax"
```

Expected logs:
```
"msg":"ccs below relax threshold","smoothed_ccs":0.72,"threshold":0.75
"msg":"quorum adjusted","old_r":1,"new_r":2,"old_w":3,"new_w":2,"reason":"relax"
```

Remove latency after testing:
```bash
kubectl exec -it acp-node-1 -- tc qdisc del dev eth0 root
```

#### 5. Verify Bounds Enforcement

System should reject adjustments that violate bounds:
```bash
# watch for bound violations
kubectl logs -f acp-node-0 | grep "adjustment rejected"
```

Expected when R hits minR=1:
```
"msg":"adjustment rejected by validation","attempted_r":0,"attempted_w":4,"error":"r=0 outside bounds [1, 5]"
```

#### 6. Verify Hysteresis (5-Second Lockout)

The system enforces a 5-second lockout between adjustments:
```bash
# check adjustment timestamps
kubectl logs acp-node-0 | grep "quorum adjusted" | tail -5
```

Adjustments should be at least 5 seconds apart.

## Configuration

Configuration is done via environment variables:

### Core Configuration

| Variable              | Description                    | Default |
|-----------------------|--------------------------------|---------|
| NODE_ID               | Unique node identifier         | node1   |
| LISTEN_ADDR           | gRPC server address            | :8080   |
| METRICS_ADDR          | Metrics server address         | :9090   |
| PEERS                 | Comma-separated peer addresses | ""      |
| QUORUM_R              | Initial read quorum size       | 2       |
| QUORUM_W              | Initial write quorum size      | 2       |
| REPLICATION_TIMEOUT   | Replication timeout            | 500ms   |
| HEALTH_PROBE_INTERVAL | Health check interval          | 500ms   |

### Kubernetes Configuration

| Variable          | Description                        | Default |
|-------------------|------------------------------------|---------|
| HEADLESS_SERVICE  | Headless service name for discovery| ""      |
| NAMESPACE         | Kubernetes namespace               | default |
| CLUSTER_SIZE      | Expected cluster size              | 3       |

### Adaptive Quorum Configuration

| Variable              | Description                              | Default |
|-----------------------|------------------------------------------|---------|
| ADAPTIVE_ENABLED      | Enable adaptive quorum system            | false   |
| MIN_R                 | Minimum read quorum size                 | 1       |
| MAX_R                 | Maximum read quorum size                 | 5       |
| MIN_W                 | Minimum write quorum size                | 2       |
| MAX_W                 | Maximum write quorum size                | 5       |
| ADAPTIVE_INTERVAL     | CCS computation and adjustment interval  | 2s      |
| CCS_RELAX_THRESHOLD   | CCS threshold to relax (decrease W)      | 0.45    |
| CCS_TIGHTEN_THRESHOLD | CCS threshold to tighten (increase W)    | 0.75    |

### CCS Formula

The Consistency Confidence Score (CCS) is computed as:

```
CCS = α×RTTHealth + β×AvailHealth + γ×VarHealth + δ×ErrorHealth + ε×ClockHealth
```

Where:
- **α = 0.20** (RTT weight)
- **β = 0.40** (Availability weight - critical for system health)
- **γ = 0.15** (Variance weight)
- **δ = 0.15** (Error weight)
- **ε = 0.10** (Clock drift weight)

Component formulas:
- **RTTHealth** = 1 - min(AvgRTT/200ms, 1)
- **AvailHealth** = Write success rate (0.0-1.0)
- **VarHealth** = 1 - min(Variance/50ms², 1)
- **ErrorHealth** = 1 - Error rate (0.0-1.0)
- **ClockHealth** = 1 - min(ClockDrift/100ms, 1)

### Adjustment Logic

- **Tighten** (CCS > `upper_bound`): W++, R-- (favors consistency when cluster is healthy)
- **Relax** (CCS < `lower_bound`): W--, R++ (favors availability when cluster is degraded)
- **Stable** (`lower_bound` ≤ CCS ≤ `upper_bound`): No adjustment
- **Hysteresis**: 5-second lockout between adjustments
- **Bounds**: minR ≤ R ≤ maxR, minW ≤ W ≤ maxW
- **Invariant**: R + W > N (quorum intersection for strong consistency)

## Prometheus Metrics

### Core Metrics

| Metric Name                    | Type      | Description                           |
|--------------------------------|-----------|---------------------------------------|
| `acp_puts_total`               | Counter   | Total PUT requests                    |
| `acp_gets_total`               | Counter   | Total GET requests                    |
| `acp_writes_success_total`     | Counter   | Successful write operations           |
| `acp_writes_failure_total`     | Counter   | Failed write operations               |
| `acp_reads_success_total`      | Counter   | Successful read operations            |
| `acp_reads_failure_total`      | Counter   | Failed read operations                |
| `acp_put_latency_seconds`      | Histogram | PUT operation latency                 |
| `acp_get_latency_seconds`      | Histogram | GET operation latency                 |
| `acp_replicate_acks_total`     | Counter   | Replication acknowledgements (success/failure) |
| `acp_replicate_latency_seconds`| Histogram | Replication latency per peer          |
| `acp_errors_total`             | Counter   | Errors by type (timeout/rpc)          |

### Adaptive Quorum Metrics

| Metric Name                              | Type    | Description                              |
|------------------------------------------|---------|------------------------------------------|
| `acp_ccs_raw`                            | Gauge   | Raw consistency confidence score         |
| `acp_ccs_smoothed`                       | Gauge   | 10-sample moving average of CCS          |
| `acp_ccs_component_rtt`                  | Gauge   | RTT health component (0.0-1.0)           |
| `acp_ccs_component_avail`                | Gauge   | Availability health component (0.0-1.0)  |
| `acp_ccs_component_var`                  | Gauge   | Variance health component (0.0-1.0)      |
| `acp_ccs_component_error`                | Gauge   | Error health component (0.0-1.0)         |
| `acp_current_r`                          | Gauge   | Current read quorum size                 |
| `acp_current_w`                          | Gauge   | Current write quorum size                |
| `acp_quorum_adjustments_total`           | Counter | Total quorum adjustments                 |
| `acp_quorum_adjustment_reason_total`     | Counter | Adjustments by reason (tighten/relax)    |
| `acp_hysteresis_active`                  | Gauge   | Whether in lockout period (0 or 1)       |

### Health Metrics

| Metric Name                    | Type    | Description                           |
|--------------------------------|---------|---------------------------------------|
| `acp_peer_health`              | Gauge   | Peer health status (1=healthy, 0=down)|
| `acp_health_probe_latency_seconds` | Histogram | Health probe latency per peer    |

## Quick Start Guide

### Local Development (Docker Compose)
```bash
# start cluster
make docker-up

# view logs
docker-compose logs -f node1

# stop cluster
make docker-down
```

### Kubernetes Cluster
```bash
# build and deploy
make build
docker build -t acp-kv:latest .
kubectl apply -f k8s/

# verify adaptive quorum is working
kubectl logs acp-node-0 | grep "adaptive quorum adjuster started"
kubectl logs -f acp-node-0 | grep smoothed_ccs

# check current quorum values
kubectl port-forward acp-node-0 9090:9090
curl http://localhost:9090/metrics | grep -E "^acp_current_(r|w)"

# scale cluster
kubectl scale statefulset acp-node --replicas=5
```

## Troubleshooting

### Adaptive Quorum Not Adjusting
- Check `ADAPTIVE_ENABLED=true` in statefulset.yaml
- Verify CCS is being computed: `kubectl logs acp-node-0 | grep ccs`
- Check if bounds are blocking: `kubectl logs acp-node-0 | grep "adjustment rejected"`
- Verify hysteresis isn't blocking: wait 5 seconds between expected adjustments

## License

MIT
