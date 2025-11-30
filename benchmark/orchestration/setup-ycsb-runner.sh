#!/bin/bash
# setup-ycsb-runner.sh - Create a YCSB runner pod inside the cluster

set -e

echo "Setting up YCSB runner pod in Kubernetes..."

# Check if pod already exists
if kubectl get pod ycsb-runner &>/dev/null; then
    echo "YCSB runner pod already exists"
else
    # Create a simple Alpine pod
    kubectl run ycsb-runner \
        --image=alpine:latest \
        --restart=Never \
        --command -- sh -c "apk add --no-cache libc6-compat && sleep infinity"

    # Wait for pod to be ready
    echo "Waiting for pod to be ready..."
    kubectl wait --for=condition=Ready pod/ycsb-runner --timeout=60s
fi

# Copy go-ycsb binary to the pod
echo "Copying go-ycsb binary to pod..."
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
YCSB_BIN="$SCRIPT_DIR/../../bin/go-ycsb"
kubectl cp "$YCSB_BIN" ycsb-runner:/usr/local/bin/go-ycsb

# Make it executable
kubectl exec ycsb-runner -- chmod +x /usr/local/bin/go-ycsb

# Copy workload files
echo "Copying workload files..."
kubectl exec ycsb-runner -- mkdir -p /workloads
for workload in "$SCRIPT_DIR/../workloads"/*.properties; do
    if [ -f "$workload" ]; then
        kubectl cp "$workload" ycsb-runner:/workloads/
    fi
done

echo "YCSB runner pod is ready!"
echo "Test with: kubectl exec ycsb-runner -- go-ycsb --help"
