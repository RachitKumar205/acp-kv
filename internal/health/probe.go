package health

import (
	"context"
	"sync"
	"time"

	"github.com/rachitkumar205/acp-kv/api/proto"
	"github.com/rachitkumar205/acp-kv/internal/metrics"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type Probe struct {
	nodeID   string
	peers    map[string]proto.ACPServiceClient // peer addr -> client
	conns    []*grpc.ClientConn
	interval time.Duration
	logger   *zap.Logger
	metrics  *metrics.Metrics
	stopCh   chan struct{}
	wg       sync.WaitGroup
}

func NewProbe(nodeID string, peerAddrs []string, interval time.Duration, logger *zap.Logger, metrics *metrics.Metrics) (*Probe, error) {
	p := &Probe{
		nodeID:   nodeID,
		peers:    make(map[string]proto.ACPServiceClient),
		conns:    make([]*grpc.ClientConn, 0),
		interval: interval,
		logger:   logger,
		metrics:  metrics,
		stopCh:   make(chan struct{}),
	}

	// establish connection to all peers
	for _, addr := range peerAddrs {
		conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			logger.Warn("failed to connect to peer for health checks", zap.String("peer", addr), zap.Error(err))
			continue
		}
		client := proto.NewACPServiceClient(conn)
		p.peers[addr] = client
		p.conns = append(p.conns, conn)
		logger.Info("health probe connected to peer", zap.String("peer", addr))
	}

	return p, nil
}

// begin periodic health checks
func (p *Probe) Start() {
	for addr := range p.peers {
		p.wg.Add(1)
		go p.probePeer(addr)
	}
}

// sends periodic health checks to a single peer
func (p *Probe) probePeer(peerAddr string) {
	defer p.wg.Done()

	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	client := p.peers[peerAddr]

	for {
		select {
		case <-p.stopCh:
			p.logger.Info("stopping health probe", zap.String("peer", peerAddr))
			return
		case <-ticker.C:
			p.checkPeer(client, peerAddr)
		}
	}
}

// performs a single health check
func (p *Probe) checkPeer(client proto.ACPServiceClient, peerAddr string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	start := time.Now()

	req := &proto.HealthRequest{
		SourceNodeId: p.nodeID,
		Timestamp:    start.UnixNano(),
	}

	resp, err := client.HealthCheck(ctx, req)
	rtt := time.Since(start)

	if err != nil {
		p.logger.Warn("health check failed",
			zap.String("peer", peerAddr),
			zap.Duration("rtt", rtt),
			zap.Error(err))
		p.metrics.Errors.WithLabelValues("health").Inc()
		// dont update RTT on failure

		return
	}

	if !resp.Healthy {
		p.logger.Warn("peer reports unhealthy",
			zap.String("peer", peerAddr),
			zap.String("peer_node_id", resp.NodeId))
		return
	}

	// record succesful health check
	p.logger.Debug("health check succeeded",
		zap.String("peer", peerAddr),
		zap.String("peer_node_id", resp.NodeId),
		zap.Duration("rtt", rtt))

	//update rtt
	p.metrics.HealthRTT.WithLabelValues(peerAddr).Set(rtt.Seconds())
}

// stop all health probes
func (p *Probe) Stop() {
	close(p.stopCh)
	p.wg.Wait()

	// close all conns
	for _, conn := range p.conns {
		if err := conn.Close(); err != nil {
			p.logger.Warn("failed to close health probe connection", zap.Error(err))
		}
	}
}
