package test

import (
	"context"
	"testing"
	"time"

	"github.com/rachitkumar205/acp-kv/api/proto"
	"github.com/rachitkumar205/acp-kv/internal/config"
	"github.com/rachitkumar205/acp-kv/internal/hlc"
	"github.com/rachitkumar205/acp-kv/internal/metrics"
	"github.com/rachitkumar205/acp-kv/internal/reconcile"
	"github.com/rachitkumar205/acp-kv/internal/replication"
	"github.com/rachitkumar205/acp-kv/internal/server"
	"github.com/rachitkumar205/acp-kv/internal/staleness"
	"github.com/rachitkumar205/acp-kv/internal/storage"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func TestThreeNodeCluster(t *testing.T) {
	// run docker-compose -f docker/docker-compose.yml up

	t.Skip("integration tests")

	ctx := context.Background()

	conn, err := grpc.NewClient("localhost:8080", grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer conn.Close()

	client := proto.NewACPServiceClient(conn)

	// test put
	putResp, err := client.Put(ctx, &proto.PutRequest{
		Key:   "test-key",
		Value: []byte("test-value"),
	})
	if err != nil {
		t.Fatalf("PUT failed: %v", err)
	}
	if !putResp.Success {
		t.Fatalf("PUT reported failure: %s", putResp.Error)
	}

	// test get
	getResp, err := client.Get(ctx, &proto.GetRequest{
		Key: "test-key",
	})
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	if !getResp.Found {
		t.Fatal("GET did not find key")
	}
	if string(getResp.Value) != "test-value" {
		t.Errorf("expected value 'test-value', got '%s'", string(getResp.Value))
	}

	t.Logf("PUT/GET successful: version=%d, timestamp=%d", getResp.Version, getResp.Timestamp)
}

func TestQuorumEnforcement(t *testing.T) {
	logger, _ := zap.NewDevelopment()

	cfg := &config.Config{
		NodeID:              "test-node",
		N:                   3,
		R:                   2,
		W:                   2,
		ReplicationTimeout:  500 * time.Millisecond,
		HealthProbeInterval: 500 * time.Millisecond,
	}

	store := storage.NewStore()
	m := metrics.NewMetrics("test")

	// test without peers
	coordinator, err := replication.NewCoordinator(cfg.NodeID, []string{}, logger, m, cfg.ReplicationTimeout)
	if err != nil {
		t.Fatalf("failed to create coordinator: %v", err)
	}
	defer coordinator.Close()

	// initialize required components
	hlcClock := hlc.NewClock(cfg.NodeID, 500*time.Millisecond)
	stalenessDetector := staleness.NewDetector(3*time.Second, m)
	reconciler := reconcile.NewEngine(store, coordinator, 30*time.Second, true, logger, m)

	srv := server.NewServer(cfg.NodeID, store, coordinator, cfg, logger, m, hlcClock, stalenessDetector, reconciler)

	// put should only succeed with local node (w=2 but only 1 node is up so the op should fail)
	ctx := context.Background()
	putResp, err := srv.Put(ctx, &proto.PutRequest{
		Key:   "test-key",
		Value: []byte("test-value"),
	})
	if err != nil {
		t.Fatalf("PUT failed with error: %v", err)
	}

	// no peers and w=2, should fail
	if putResp.Success {
		t.Error("expected PUT to fail due to insufficient nodes, but it somehow succeeded")
	}

	t.Logf("quorum enforcement working: PUT correctly failed with insufficient nodes")
}

func TestConcurrentOperations(t *testing.T) {
	logger, _ := zap.NewDevelopment()

	cfg := &config.Config{
		NodeID:              "test-node",
		N:                   1,
		R:                   1,
		W:                   1,
		ReplicationTimeout:  500 * time.Millisecond,
		HealthProbeInterval: 500 * time.Millisecond,
	}

	store := storage.NewStore()
	m := metrics.NewMetrics("test")
	coordinator, _ := replication.NewCoordinator(cfg.NodeID, []string{}, logger, m, cfg.ReplicationTimeout)
	defer coordinator.Close()

	// initialize required components
	hlcClock := hlc.NewClock(cfg.NodeID, 500*time.Millisecond)
	stalenessDetector := staleness.NewDetector(3*time.Second, m)
	reconciler := reconcile.NewEngine(store, coordinator, 30*time.Second, true, logger, m)

	srv := server.NewServer(cfg.NodeID, store, coordinator, cfg, logger, m, hlcClock, stalenessDetector, reconciler)
	ctx := context.Background()

	numOps := 50
	done := make(chan bool, numOps)

	for i := 0; i < numOps; i++ {
		go func(id int) {
			key := string(rune('a' + id%26))
			_, err := srv.Put(ctx, &proto.PutRequest{
				Key:   key,
				Value: []byte{byte(id)},
			})
			if err != nil {
				t.Errorf("PUT failed: %v", err)
			}
			done <- true
		}(i)
	}

	// wait for all ops
	for i := 0; i < numOps; i++ {
		<-done
	}

	// verify we can read values
	getResp, err := srv.Get(ctx, &proto.GetRequest{Key: "a"})
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	if !getResp.Found {
		t.Error("expected to find key 'a'")
	}

	t.Logf("Concurrent operations test passed: %d operations completed", numOps)
}

func TestHealthCheck(t *testing.T) {
	logger, _ := zap.NewDevelopment()

	cfg := &config.Config{
		NodeID: "test-node",
		N:      1,
		R:      1,
		W:      1,
	}

	store := storage.NewStore()
	m := metrics.NewMetrics("test")
	coordinator, _ := replication.NewCoordinator(cfg.NodeID, []string{}, logger, m, 500*time.Millisecond)
	defer coordinator.Close()

	// initialize required components
	hlcClock := hlc.NewClock(cfg.NodeID, 500*time.Millisecond)
	stalenessDetector := staleness.NewDetector(3*time.Second, m)
	reconciler := reconcile.NewEngine(store, coordinator, 30*time.Second, true, logger, m)

	srv := server.NewServer(cfg.NodeID, store, coordinator, cfg, logger, m, hlcClock, stalenessDetector, reconciler)
	ctx := context.Background()

	healthResp, err := srv.HealthCheck(ctx, &proto.HealthRequest{
		SourceNodeId: "other-node",
		Timestamp:    time.Now().UnixNano(),
	})
	if err != nil {
		t.Fatalf("healthcheck failed: %v", err)
	}
	if !healthResp.Healthy {
		t.Error("expected node to be healthy")
	}
	if healthResp.NodeId != cfg.NodeID {
		t.Errorf("expected node_id=%s, got %s", cfg.NodeID, healthResp.NodeId)
	}

	t.Logf("health check passed: node=%s, healthy=%v", healthResp.NodeId, healthResp.Healthy)
}

func TestReplication(t *testing.T) {
	logger, _ := zap.NewDevelopment()

	cfg := &config.Config{
		NodeID: "test-node",
		N:      1,
		R:      1,
		W:      1,
	}

	store := storage.NewStore()
	m := metrics.NewMetrics("test")
	coordinator, _ := replication.NewCoordinator(cfg.NodeID, []string{}, logger, m, 500*time.Millisecond)
	defer coordinator.Close()

	// initialize required components
	hlcClock := hlc.NewClock(cfg.NodeID, 500*time.Millisecond)
	stalenessDetector := staleness.NewDetector(3*time.Second, m)
	reconciler := reconcile.NewEngine(store, coordinator, 30*time.Second, true, logger, m)

	srv := server.NewServer(cfg.NodeID, store, coordinator, cfg, logger, m, hlcClock, stalenessDetector, reconciler)
	ctx := context.Background()

	// test replicatoin
	repResp, err := srv.Replicate(ctx, &proto.ReplicateRequest{
		Key:          "rep-key",
		Value:        []byte("rep-value"),
		Version:      12345,
		Timestamp:    time.Now().UnixNano(),
		SourceNodeId: "other-node",
	})
	if err != nil {
		t.Fatalf("Replicate failed: %v", err)
	}
	if !repResp.Success {
		t.Error("expected replication to succeed")
	}

	// verify data stored
	vv, found := store.Get("rep-key")
	if !found {
		t.Fatal("expected replicated key to be stored")
	}
	if string(vv.Value) != "rep-value" {
		t.Errorf("expected value 'rep-value', got '%s'", string(vv.Value))
	}

	t.Logf("Replication test passed: key stored successfully")

}
