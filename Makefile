.PHONY: help proto build test run clean docker-build docker-up docker-down docker-logs

help:
	@echo 'Usage: make [target]'
	@echo ''
	@echo 'Available targets:'
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-15s %s\n", $$1, $$2}'

proto:
	protoc --go_out=. --go_opt=paths=source_relative \
		--go-grpc_out=. --go-grpc_opt=paths=source_relative \
		api/proto/acp.proto

build: proto
	go build -o bin/acp-node ./cmd/acp-node

test:
	go test ./internal/storage -v
	go test ./internal/replication -v

test-integration:
	go test ./test -v

run: build
	./bin/acp-node

clean:
	rm -rf bin/
	rm -f api/proto/*.pb.go

docker-build:
	cd docker && docker-compose build

docker-up:
	cd docker && docker-compose up -d

docker-down:
	cd docker && docker-compose down

docker-logs:
	cd docker && docker-compose logs -f

docker-restart: docker-down docker-up

deps:
	go mod download
	go mod tidy

install-tools:
	go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
	go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest

fmt:
	go fmt ./...

# Build custom adaptive benchmark
acp-bench:
	go build -o bin/acp-bench ./cmd/acp-adaptive-bench

# Use the custom adaptive benchmark for testing (recommended)
ycsb-test:
	@echo "Note: go-ycsb needs custom build with ACP binding."
	@echo "Instead, use the custom adaptive benchmark:"
	@echo "  make acp-bench"
	@echo "  ./bin/acp-bench --help"

k8s-build:
	docker build -f docker/Dockerfile -t acp-kv:latest .

k8s-load-kind:
	kind load docker-image acp-kv:latest --name kind

k8s-deploy:
	kubectl apply -f k8s/

k8s-delete:
	kubectl delete -f k8s/

k8s-restart: k8s-delete k8s-deploy

k8s-scale:
	@read -p "Enter number of replicas: " replicas; \
	echo "Scaling to $$replicas nodes..."; \
	kubectl scale statefulset acp-node --replicas=$$replicas && \
	kubectl set env statefulset/acp-node CLUSTER_SIZE=$$replicas && \
	echo "Waiting for pods to be ready..." && \
	kubectl rollout status statefulset acp-node && \
	echo "✓ Cluster scaled to $$replicas nodes!"

k8s-scale-to:
	@if [ -z "$(REPLICAS)" ]; then \
		echo "Usage: make k8s-scale-to REPLICAS=5"; \
		exit 1; \
	fi
	@echo "Scaling to $(REPLICAS) nodes..."
	@kubectl scale statefulset acp-node --replicas=$(REPLICAS)
	@kubectl set env statefulset/acp-node CLUSTER_SIZE=$(REPLICAS)
	@kubectl rollout status statefulset acp-node
	@echo "✓ Cluster scaled to $(REPLICAS) nodes!"

k8s-logs:
	kubectl logs -f -l app=acp-node --all-containers=true

k8s-status:
	kubectl get pods,svc,statefulsets

k8s-port-forward:
	kubectl port-forward svc/acp-service 8080:8080

.DEFAULT_GOAL := help
