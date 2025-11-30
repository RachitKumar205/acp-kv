package replication

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/rachitkumar205/acp-kv/internal/hlc"
	"github.com/rachitkumar205/acp-kv/internal/metrics"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/rachitkumar205/acp-kv/api/proto"
	"go.uber.org/zap"
)

// manages replication to peer nodes
type Coordinator struct {
	nodeID            string
	peers             map[string]proto.ACPServiceClient // peer address -> client
	conns             map[string]*grpc.ClientConn       // peer address -> connection
	configuredPeers   []string                          // full list of configured peer addresses (immutable)
	logger            *zap.Logger
	metrics           *metrics.Metrics
	timeout           time.Duration
	mu                sync.RWMutex // protect peers and conns maps
}

func NewCoordinator(nodeID string, peerAddrs []string, logger *zap.Logger, metrics *metrics.Metrics, timeout time.Duration) (*Coordinator, error) {
	c := &Coordinator{
		nodeID:          nodeID,
		peers:           make(map[string]proto.ACPServiceClient),
		conns:           make(map[string]*grpc.ClientConn),
		configuredPeers: peerAddrs, // store full configured list
		logger:          logger,
		metrics:         metrics,
		timeout:         timeout,
	}

	// est connections to all peers
	for _, addr := range peerAddrs {
		if err := c.addPeer(addr); err != nil {
			logger.Warn("failed to connect to peer", zap.String("peer", addr), zap.Error(err))
		}
	}

	return c, nil
}

func (c *Coordinator) addPeer(addr string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// check if already connected
	if _, exists := c.peers[addr]; exists {
		return nil
	}

	// Use dns:/// scheme for Kubernetes DNS resolution
	target := "dns:///" + addr
	conn, err := grpc.NewClient(target, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return err
	}

	client := proto.NewACPServiceClient(conn)
	c.peers[addr] = client
	c.conns[addr] = conn
	c.logger.Info("connected to peer", zap.String("peer", addr))
	return nil
}

func (c *Coordinator) removePeer(addr string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if conn, exists := c.conns[addr]; exists {
		conn.Close()
		delete(c.peers, addr)
		delete(c.conns, addr)
		c.logger.Info("removed peer", zap.String("peer", addr))
	}
}

// uses dns lookup to find all running pods
func DiscoverPeersDNS(nodeID, headlessSvc, namespace string) ([]string, error) {
	fqdn := fmt.Sprintf("%s.%s.svc.cluster.local", headlessSvc, namespace)

	// lookuphost returns ips of all ready pods
	ips, err := net.LookupHost(fqdn)
	if err != nil {
		return nil, fmt.Errorf("dns lookup failed for %s: %w", fqdn, err)
	}

	peers := []string{}
	headlessPattern := fmt.Sprintf(".%s.%s.svc.cluster.local", headlessSvc, namespace)

	for _, ip := range ips {
		// reverse lookup to get pod name
		names, err := net.LookupAddr(ip)
		if err != nil || len(names) == 0 {
			// if reverse lookup fails, skip
			continue
		}

		// find the statefulset pod name (not the ip-based service name)
		// names contains entries like:
		//   "acp-node-2.acp-headless.default.svc.cluster.local."
		//   "10-244-0-44.acp-service.default.svc.cluster.local."
		// we want the one matching the headless service
		var podFQDN string
		for _, name := range names {
			if strings.Contains(name, headlessPattern) {
				podFQDN = name
				break
			}
		}

		if podFQDN == "" {
			continue // no valid pod name found
		}

		// extract pod name from fqdn
		parts := strings.Split(podFQDN, ".")
		if len(parts) < 2 {
			continue
		}

		podName := parts[0] // "acp-node-0"
		if podName == nodeID {
			continue // skip self
		}

		// construct full peer address
		peerAddr := fmt.Sprintf("%s.%s.%s.svc.cluster.local:8080",
			podName, headlessSvc, namespace)
		peers = append(peers, peerAddr)
	}

	return peers, nil
}

func (c *Coordinator) reconcilePeers(newPeerAddrs []string) {
	newPeerSet := make(map[string]bool)
	for _, addr := range newPeerAddrs {
		newPeerSet[addr] = true
	}

	// get current peers
	c.mu.RLock()
	currentPeers := make([]string, 0, len(c.peers))
	for addr := range c.peers {
		currentPeers = append(currentPeers, addr)
	}
	c.mu.RUnlock()

	// remove peers that no longer exist
	for _, addr := range currentPeers {
		if !newPeerSet[addr] {
			c.removePeer(addr)
		}
	}

	// add new peers
	for addr := range newPeerSet {
		c.mu.RLock()
		_, exists := c.peers[addr]
		c.mu.RUnlock()

		if !exists {
			if err := c.addPeer(addr); err != nil {
				c.logger.Warn("failed to connect to new peer",
					zap.String("peer", addr),
					zap.Error(err))
			}
		}
	}
}

func (c *Coordinator) StartPeerDiscovery(ctx context.Context, nodeID, headlessSvc, namespace string, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	c.logger.Info("starting peer discovery",
		zap.String("method", "dns"),
		zap.Duration("interval", interval))

	for {
		select {
		case <-ticker.C:
			peers, err := DiscoverPeersDNS(nodeID, headlessSvc, namespace)
			if err != nil {
				c.logger.Warn("peer discovery failed", zap.Error(err))
				continue
			}

			c.logger.Debug("discovered peers",
				zap.Int("count", len(peers)),
				zap.Strings("peers", peers))

			c.reconcilePeers(peers)

		case <-ctx.Done():
			c.logger.Info("peer discovery stopped")
			return
		}
	}
}

// getpeeraddresses returns list of all current peer addresses
func (c *Coordinator) GetPeerAddresses() []string {
	// Return full configured peer list, not just connected peers
	// This ensures CCS calculation uses N=total_cluster_size, not just reachable peers
	return c.configuredPeers
}

// GetConnectedPeerAddresses returns only currently connected peers
func (c *Coordinator) GetConnectedPeerAddresses() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	addrs := make([]string, 0, len(c.peers))
	for addr := range c.peers {
		addrs = append(addrs, addr)
	}
	return addrs
}

func (c *Coordinator) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

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
func (c *Coordinator) Replicate(ctx context.Context, key string, value []byte, version, timestamp int64, hlcTimestamp hlc.HLC, requiredAcks int) (int, []ReplicateResult, error) {
	// get snapshot of current peers
	c.mu.RLock()
	peerList := make(map[string]proto.ACPServiceClient, len(c.peers))
	for addr, client := range c.peers {
		peerList[addr] = client
	}
	c.mu.RUnlock()

	if len(peerList) == 0 {
		// no peers, only self acknowledgement
		return 1, []ReplicateResult{}, nil
	}

	results := make(chan ReplicateResult, len(peerList))
	var wg sync.WaitGroup

	//send replication requests to all peers in parallel
	for addr, client := range peerList {
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
				Hlc:          hlcTimestamp.ToProto(),
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
		zap.Int("total_peers", len(peerList)))

	if successCount < requiredAcks {
		return successCount, allResults, fmt.Errorf("insufficient acknowledgements: got %d, need %d", successCount, requiredAcks)
	}

	return successCount, allResults, nil
}

// query R replicas for a key and return all versions
func (c *Coordinator) QueryReplicas(ctx context.Context, key string, requiredResponses int) ([]ReplicaValue, error) {
	// get snapshot of current peers
	c.mu.RLock()
	peerList := make(map[string]proto.ACPServiceClient, len(c.peers))
	for addr, client := range c.peers {
		peerList[addr] = client
	}
	c.mu.RUnlock()

	if len(peerList) == 0 {
		if requiredResponses > 1 {
			return nil, fmt.Errorf("insufficient replicas: need %d, have only self", requiredResponses)
		}
		return []ReplicaValue{}, nil
	}

	results := make(chan ReplicaValue, len(peerList))
	var wg sync.WaitGroup

	// query all peers parallel
	for addr, client := range peerList {
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
					HLC:       hlc.FromProto(resp.Hlc),
					IsStale:   resp.IsStale,
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
	HLC       hlc.HLC // hybrid logical clock timestamp
	IsStale   bool    // indicates if data exceeds staleness bound
	Found     bool
}

// get most recent val based on hlc timestamp (lww using hlc)
func GetMostRecent(values []ReplicaValue) (ReplicaValue, bool) {
	if len(values) == 0 {
		return ReplicaValue{}, false
	}

	mostRecent := values[0]
	for _, v := range values[1:] {
		// use hlc comparison for proper causality tracking
		if v.HLC.HappensAfter(mostRecent.HLC) {
			mostRecent = v
		}
	}

	return mostRecent, true
}
