package health

import (
	"context"
	"sync"
	"time"

	"github.com/rachitkumar205/acp-kv/api/proto"
	"github.com/rachitkumar205/acp-kv/internal/metrics"
	"github.com/rachitkumar205/acp-kv/internal/replication"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// healinglistener receives notifications when partitions heal
type HealingListener interface {
	NotifyHealingEvent(peer string)
}

type Probe struct {
	nodeID          string
	peers           map[string]proto.ACPServiceClient // peer addr -> client
	conns           map[string]*grpc.ClientConn
	interval        time.Duration
	logger          *zap.Logger
	metrics         *metrics.Metrics
	stopCh          chan struct{}
	wg              sync.WaitGroup
	mu              sync.RWMutex                   // protect peers and conns maps
	probes          map[string]context.CancelFunc  // track active probe goroutines
	peerStatus      map[string]bool                // track peer up/down status
	healingListener HealingListener                // notified on partition healing
}

func NewProbe(nodeID string, peerAddrs []string, interval time.Duration, logger *zap.Logger, metrics *metrics.Metrics) (*Probe, error) {
	p := &Probe{
		nodeID:     nodeID,
		peers:      make(map[string]proto.ACPServiceClient),
		conns:      make(map[string]*grpc.ClientConn),
		interval:   interval,
		logger:     logger,
		metrics:    metrics,
		stopCh:     make(chan struct{}),
		probes:     make(map[string]context.CancelFunc),
		peerStatus: make(map[string]bool),
	}

	// establish connection to all peers
	for _, addr := range peerAddrs {
		if err := p.addPeer(addr); err != nil {
			logger.Warn("failed to connect to peer for health checks", zap.String("peer", addr), zap.Error(err))
		}
	}

	return p, nil
}

// sethealinglistener sets the listener for partition healing events
func (p *Probe) SetHealingListener(listener HealingListener) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.healingListener = listener
}

func (p *Probe) addPeer(addr string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if _, exists := p.peers[addr]; exists {
		return nil
	}

	// Use dns:/// scheme for Kubernetes DNS resolution
	target := "dns:///" + addr
	conn, err := grpc.NewClient(target, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return err
	}

	client := proto.NewACPServiceClient(conn)
	p.peers[addr] = client
	p.conns[addr] = conn
	p.logger.Info("health probe connected to peer", zap.String("peer", addr))
	return nil
}

func (p *Probe) removePeer(addr string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	// stop the probe goroutine
	if cancel, exists := p.probes[addr]; exists {
		cancel()
		delete(p.probes, addr)
	}

	// close connection
	if conn, exists := p.conns[addr]; exists {
		conn.Close()
		delete(p.peers, addr)
		delete(p.conns, addr)
		p.logger.Info("health probe removed peer", zap.String("peer", addr))
	}
}

func (p *Probe) reconcilePeers(ctx context.Context, newPeerAddrs []string) {
	newPeerSet := make(map[string]bool)
	for _, addr := range newPeerAddrs {
		newPeerSet[addr] = true
	}

	// remove old peers
	p.mu.RLock()
	existingPeers := make([]string, 0, len(p.peers))
	for addr := range p.peers {
		existingPeers = append(existingPeers, addr)
	}
	p.mu.RUnlock()

	for _, addr := range existingPeers {
		if !newPeerSet[addr] {
			p.removePeer(addr)
		}
	}

	// add new peers
	for addr := range newPeerSet {
		p.mu.RLock()
		_, exists := p.peers[addr]
		p.mu.RUnlock()

		if !exists {
			if err := p.addPeer(addr); err != nil {
				p.logger.Warn("failed to connect to new peer for health probe",
					zap.String("peer", addr),
					zap.Error(err))
			} else {
				// start probing the new peer
				go p.probePeer(ctx, addr)
			}
		}
	}
}

func (p *Probe) StartPeerDiscovery(ctx context.Context, nodeID, headlessSvc, namespace string, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	p.logger.Info("starting health probe peer discovery",
		zap.String("method", "dns"),
		zap.Duration("interval", interval))

	for {
		select {
		case <-ticker.C:
			peers, err := replication.DiscoverPeersDNS(nodeID, headlessSvc, namespace)
			if err != nil {
				p.logger.Warn("health probe peer discovery failed", zap.Error(err))
				continue
			}

			p.reconcilePeers(ctx, peers)

		case <-ctx.Done():
			p.logger.Info("health probe discovery stopped")
			return
		}
	}
}

// begin periodic health checks
func (p *Probe) Start() {
	p.mu.RLock()
	for addr := range p.peers {
		p.wg.Add(1)
		go p.probePeerLegacy(addr)
	}
	p.mu.RUnlock()
}

// sends periodic health checks to a single peer (legacy version)
func (p *Probe) probePeerLegacy(peerAddr string) {
	defer p.wg.Done()

	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	for {
		select {
		case <-p.stopCh:
			p.logger.Info("stopping health probe", zap.String("peer", peerAddr))
			return
		case <-ticker.C:
			p.mu.RLock()
			client, exists := p.peers[peerAddr]
			p.mu.RUnlock()

			if !exists {
				return // peer removed
			}

			p.checkPeer(client, peerAddr)
		}
	}
}

// sends periodic health checks with cancellable context for dynamic discovery
func (p *Probe) probePeer(ctx context.Context, peerAddr string) {
	// create cancellable context for this probe
	probeCtx, cancel := context.WithCancel(ctx)

	p.mu.Lock()
	p.probes[peerAddr] = cancel
	p.mu.Unlock()

	defer func() {
		p.mu.Lock()
		delete(p.probes, peerAddr)
		p.mu.Unlock()
	}()

	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			p.mu.RLock()
			client, exists := p.peers[peerAddr]
			p.mu.RUnlock()

			if !exists {
				return // peer removed
			}

			p.checkPeer(client, peerAddr)

		case <-probeCtx.Done():
			return
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

	// check previous status for partition healing detection
	p.mu.RLock()
	wasDown := !p.peerStatus[peerAddr]
	p.mu.RUnlock()

	if err != nil {
		p.logger.Warn("health check failed",
			zap.String("peer", peerAddr),
			zap.Duration("rtt", rtt),
			zap.Error(err))
		p.metrics.Errors.WithLabelValues("health").Inc()

		// mark peer as down
		p.mu.Lock()
		p.peerStatus[peerAddr] = false
		p.mu.Unlock()

		return
	}

	if !resp.Healthy {
		p.logger.Warn("peer reports unhealthy",
			zap.String("peer", peerAddr),
			zap.String("peer_node_id", resp.NodeId))

		// mark peer as down
		p.mu.Lock()
		p.peerStatus[peerAddr] = false
		p.mu.Unlock()

		return
	}

	// peer is now healthy
	p.mu.Lock()
	p.peerStatus[peerAddr] = true
	p.mu.Unlock()

	// detect partition healing: peer was down, now up
	if wasDown && p.healingListener != nil {
		p.logger.Info("partition healing detected",
			zap.String("peer", peerAddr),
			zap.String("peer_node_id", resp.NodeId))
		p.healingListener.NotifyHealingEvent(peerAddr)
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

	p.mu.Lock()
	defer p.mu.Unlock()

	// close all conns
	for _, conn := range p.conns {
		if err := conn.Close(); err != nil {
			p.logger.Warn("failed to close health probe connection", zap.Error(err))
		}
	}
}
