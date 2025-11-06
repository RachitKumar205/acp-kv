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

.DEFAULT_GOAL := help
