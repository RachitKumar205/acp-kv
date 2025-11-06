# ACP-Key Value Store

A distributed multi node key-value store with configurable quorum based replication. Eventually down the line it's going to have quorum parameters that adapt in real time based on how the network is performing.

### Build and Run

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

## Testing

Run unit tests:
```bash
go test ./internal/storage -v
go test ./internal/replication -v
```

Run integration tests (requires running cluster):
```bash
go test ./test -v
```

## Configuration

Configuration is done via environment variables:

| Variable              | Description                    | Default |
|-----------------------|--------------------------------|---------|
| NODE_ID               | Unique node identifier         | node1   |
| LISTEN_ADDR           | gRPC server address            | :8080   |
| METRICS_ADDR          | Metrics server address         | :9090   |
| PEERS                 | Comma-separated peer addresses | ""      |
| QUORUM_R              | Read quorum size               | 2       |
| QUORUM_W              | Write quorum size              | 2       |
| REPLICATION_TIMEOUT   | Replication timeout            | 500ms   |
| HEALTH_PROBE_INTERVAL | Health check interval          | 500ms   |
