package replication

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/rachitkumar205/acp-kv/internal/metrics"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/rachitkumar205/acp-kv/api/proto"
	"go.uber.org/zap"
)

// manages replication to peer nodes
type Coordinator struct {
	nodeID  string
	peers   map[string]proto.ACPServiceClient // peer address -> client
	conns   []*grpc.ClientConn                // track conns
	logger  *zap.Logger
	metrics *metrics.Metrics
	timeout time.Duration
}

func NewCoordinator(nodeID string, peerAddrs []string, logger *zap.Logger, metrics *metrics.Metrics, timeout time.Duration) (*Coordinator, error) {
	c := &Coordinator{
		nodeID:  nodeID,
		peers:   make(map[string]proto.ACPServiceClient),
		logger:  logger,
		metrics: metrics,
		timeout: timeout,
	}

	// est connections to all pairs
	for _, addr := range peerAddrs {
		conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))

		if err != nil {
			logger.Warn("failed to connect to peer", zap.String("peer", addr), zap.Error(err))
			continue
		}

		client := proto.NewACPServiceClient(conn)
		c.peers[addr] = client
		c.conns = append(c.conns, conn)
		logger.Info("connected to peer", zap.String("peer", addr))
	}

	return c, nil
}

func (c *Coordinator) Close() error {
	for _, conn := range c.conns {
		if err := conn.Close(); err != nil {
			c.logger.Warn("failed to close connection", zap.Error(err))
		}
	}

	return nil
}

// holds the result of a single replication attempt
type ReplicateResult struct {
	PeerAddr string
	Success  bool
	Latency  time.Duration
	Error    error
}

// send replication requests to all peers and wait for W acks
func (c *Coordinator) Replicate(ctx context.Context, key string, value []byte, version, timestamp int64, requiredAcks int) (int, []ReplicateResult, error) {
	if len(c.peers) == 0 {
		// no peers, only self acknowledgement
		return 1, []ReplicateResult{}, nil
	}

	results := make(chan ReplicateResult, len(c.peers))
	var wg sync.WaitGroup

	//send replication requests to all peers in parallel
	for addr, client := range c.peers {
		wg.Add(1)
		go func(peerAddr string, peerClient proto.ACPServiceClient) {
			defer wg.Done()

			start := time.Now()
			repCtx, cancel := context.WithTimeout(ctx, c.timeout)
			defer cancel()

			req := &proto.ReplicateRequest{
				Key:          key,
				Value:        value,
				Version:      version,
				Timestamp:    timestamp,
				SourceNodeId: c.nodeID,
			}

			resp, err := peerClient.Replicate(repCtx, req)
			latency := time.Since(start)

			result := ReplicateResult{
				PeerAddr: peerAddr,
				Latency:  latency,
			}

			if err != nil {
				result.Error = err
				result.Success = false
				c.logger.Warn("replication failed",
					zap.String("peer", peerAddr),
					zap.String("key", key),
					zap.Duration("latency", latency),
					zap.Error(err))
				c.metrics.ReplicateAcks.WithLabelValues("failure").Inc()
				c.metrics.Errors.WithLabelValues("rpc").Inc()
			} else if !resp.Success {
				result.Error = fmt.Errorf("peer reported failure: %s", resp.Error)
				result.Success = false
				c.logger.Warn("replication rejected by peer",
					zap.String("peer", peerAddr),
					zap.String("key", key),
					zap.String("error", resp.Error))
				c.metrics.ReplicateAcks.WithLabelValues("failure").Inc()
			} else {
				result.Success = true
				c.logger.Debug("replication succeeded",
					zap.String("peer", peerAddr),
					zap.String("key", key),
					zap.Duration("latency", latency))
				c.metrics.ReplicateAcks.WithLabelValues("success").Inc()
			}

			// record latency
			c.metrics.ReplicateLatency.WithLabelValues(peerAddr).Observe(latency.Seconds())

			results <- result
		}(addr, client)
	}

	// close results chan when all goroutines complete
	go func() {
		wg.Wait()
		close(results)
	}()

	// collect results
	var allResults []ReplicateResult
	successCount := 1 // 1 for self ack

	for result := range results {
		allResults = append(allResults, result)
		if result.Success {
			successCount++
		}
	}

	c.logger.Info("replication completed",
		zap.String("key", key),
		zap.Int("success_count", successCount),
		zap.Int("required_acks", requiredAcks),
		zap.Int("total_peers", len(c.peers)))

	if successCount < requiredAcks {
		return successCount, allResults, fmt.Errorf("insufficient acknowledgements: got %d, need %d", successCount, requiredAcks)
	}

	return successCount, allResults, nil
}

// query R replicas for a key and return all versions
func (c *Coordinator) QueryReplicas(ctx context.Context, key string, requiredResponses int) ([]ReplicaValue, error) {
	if len(c.peers) == 0 {
		if requiredResponses > 1 {
			return nil, fmt.Errorf("insufficient replicas: need %d, have only self", requiredResponses)
		}
		return []ReplicaValue{}, nil
	}

	results := make(chan ReplicaValue, len(c.peers))
	var wg sync.WaitGroup

	// query all peers parallel
	for addr, client := range c.peers {
		wg.Add(1)
		go func(peerAddr string, peerClient proto.ACPServiceClient) {
			defer wg.Done()

			queryCtx, cancel := context.WithTimeout(ctx, c.timeout)
			defer cancel()

			req := &proto.GetRequest{Key: key}
			resp, err := peerClient.GetLocal(queryCtx, req)

			if err != nil {
				c.logger.Warn("query failed",
					zap.String("peer", peerAddr),
					zap.String("key", key),
					zap.Error(err))
				c.metrics.Errors.WithLabelValues("rpc").Inc()
				return
			}

			if resp.Found {
				results <- ReplicaValue{
					PeerAddr:  peerAddr,
					Value:     resp.Value,
					Version:   resp.Version,
					Timestamp: resp.Timestamp,
					Found:     true,
				}
			}
		}(addr, client)
	}

	// close results chan when all goroutines complete
	go func() {
		wg.Wait()
		close(results)
	}()

	// collect results
	var allResults []ReplicaValue
	for result := range results {
		allResults = append(allResults, result)
	}

	// add 1 for self
	totalResponses := len(allResults) + 1

	if totalResponses < requiredResponses {
		return nil, fmt.Errorf("insufficient responses: got %d, need %d", totalResponses, requiredResponses)
	}

	return allResults, nil
}

// value returned from a replica
type ReplicaValue struct {
	PeerAddr  string
	Value     []byte
	Version   int64
	Timestamp int64
	Found     bool
}

// get most recent val based on timestamp
func GetMostRecent(values []ReplicaValue) (ReplicaValue, bool) {
	if len(values) == 0 {
		return ReplicaValue{}, false
	}

	mostRecent := values[0]
	for _, v := range values[1:] {
		if v.Timestamp > mostRecent.Timestamp {
			mostRecent = v
		}
	}

	return mostRecent, true
}
