#!/bin/bash
# restore-network.sh - Remove all network chaos from ACP pods
# Usage: ./restore-network.sh [pod-name]

set -e

if [ $# -eq 0 ]; then
    # restore all acp-node pods
    PODS=$(kubectl get pods -l app=acp-node -o jsonpath='{.items[*].metadata.name}')
    if [ -z "$PODS" ]; then
        echo "no acp-node pods found"
        exit 0
    fi
else
    PODS="$*"
fi

echo "restoring network for pods: $PODS"

for POD in $PODS; do
    echo "restoring $POD..."

    # check if pod exists
    if ! kubectl get pod "$POD" &>/dev/null; then
        echo "  warning: pod $POD not found, skipping"
        continue
    fi

    # remove tc rules (ignore errors if none exist)
    kubectl exec "$POD" -- tc qdisc del dev eth0 root 2>/dev/null || echo "  no tc rules to remove"

    # flush iptables rules (ignore errors if none exist)
    kubectl exec "$POD" -- iptables -F 2>/dev/null || echo "  no iptables rules to remove"

    echo "  $POD restored"
done

echo "network restoration complete"
