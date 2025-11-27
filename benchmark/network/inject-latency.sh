#!/bin/bash
# inject-latency.sh - Add network latency to ACP pods
# Usage: ./inject-latency.sh <pod-name> <delay-ms> [jitter-ms]

set -e

if [ $# -lt 2 ]; then
    echo "usage: $0 <pod-name> <delay-ms> [jitter-ms]"
    echo "example: $0 acp-node-0 150 20"
    exit 1
fi

POD=$1
DELAY=$2
JITTER=${3:-0}

echo "injecting latency to pod $POD: delay=${DELAY}ms jitter=${JITTER}ms"

# check if pod exists
if ! kubectl get pod "$POD" &>/dev/null; then
    echo "error: pod $POD not found"
    exit 1
fi

# inject latency using tc netem
if [ "$JITTER" = "0" ]; then
    kubectl exec "$POD" -- tc qdisc add dev eth0 root netem delay "${DELAY}ms"
else
    kubectl exec "$POD" -- tc qdisc add dev eth0 root netem delay "${DELAY}ms" "${JITTER}ms"
fi

echo "latency injected successfully"
echo "to remove: kubectl exec $POD -- tc qdisc del dev eth0 root"
