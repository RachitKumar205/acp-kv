#!/bin/bash
# init-cluster.sh - Initialize Redis cluster after StatefulSet is ready
set -e

echo "Waiting for Redis pods to be ready..."
kubectl wait --for=condition=ready pod -l app=redis-bench --timeout=120s

echo "Getting Redis pod IPs..."
POD0_IP=$(kubectl get pod redis-bench-0 -o jsonpath='{.status.podIP}')
POD1_IP=$(kubectl get pod redis-bench-1 -o jsonpath='{.status.podIP}')
POD2_IP=$(kubectl get pod redis-bench-2 -o jsonpath='{.status.podIP}')

echo "Redis pods:"
echo "  redis-bench-0: $POD0_IP"
echo "  redis-bench-1: $POD1_IP"
echo "  redis-bench-2: $POD2_IP"

echo "Creating Redis cluster..."
kubectl exec redis-bench-0 -- redis-cli --cluster create \
  ${POD0_IP}:6379 \
  ${POD1_IP}:6379 \
  ${POD2_IP}:6379 \
  --cluster-replicas 0 \
  --cluster-yes

echo "Verifying cluster status..."
kubectl exec redis-bench-0 -- redis-cli cluster info

echo "Redis cluster initialized successfully!"
