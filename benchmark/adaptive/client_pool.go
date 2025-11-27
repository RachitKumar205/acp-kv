package adaptive

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/rachitkumar205/acp-kv/api/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// ClientPool manages a pool of ACP gRPC client connections with round-robin load balancing
type ClientPool struct {
	clients []*grpc.ClientConn
	index   atomic.Uint32
	mu      sync.RWMutex
}

// NewClientPool creates a new client pool with connections to all endpoints
func NewClientPool(endpoints []string) (*ClientPool, error) {
	if len(endpoints) == 0 {
		return nil, fmt.Errorf("no endpoints provided")
	}

	pool := &ClientPool{
		clients: make([]*grpc.ClientConn, 0, len(endpoints)),
	}

	// connect to all endpoints
	for _, endpoint := range endpoints {
		conn, err := grpc.Dial(endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			// close any already-opened connections
			pool.Close()
			return nil, fmt.Errorf("failed to connect to %s: %w", endpoint, err)
		}
		pool.clients = append(pool.clients, conn)
	}

	return pool, nil
}

// Get returns the next client in round-robin fashion
func (p *ClientPool) Get() Client {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if len(p.clients) == 0 {
		return nil
	}

	// round-robin selection
	idx := p.index.Add(1) % uint32(len(p.clients))
	return &grpcClient{
		client: proto.NewACPServiceClient(p.clients[idx]),
	}
}

// HealthCheck verifies all connections are healthy
func (p *ClientPool) HealthCheck(ctx context.Context) error {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if len(p.clients) == 0 {
		return fmt.Errorf("no clients available")
	}

	// test each connection with a dummy operation
	for i, conn := range p.clients {
		client := proto.NewACPServiceClient(conn)
		_, err := client.Get(ctx, &proto.GetRequest{Key: "__health_check__"})
		if err != nil && err.Error() != "rpc error: code = Unknown desc = key not found" {
			return fmt.Errorf("health check failed for client %d: %w", i, err)
		}
	}

	return nil
}

// Close closes all client connections
func (p *ClientPool) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()

	for _, conn := range p.clients {
		conn.Close()
	}
	p.clients = nil
}

// Client represents a single ACP client
type Client interface {
	Get(ctx context.Context, key string) ([]byte, error)
	Put(ctx context.Context, key string, value []byte) (int64, error)
}

// grpcClient implements Client using gRPC
type grpcClient struct {
	client proto.ACPServiceClient
}

func (c *grpcClient) Get(ctx context.Context, key string) ([]byte, error) {
	resp, err := c.client.Get(ctx, &proto.GetRequest{Key: key})
	if err != nil {
		return nil, err
	}
	return resp.Value, nil
}

func (c *grpcClient) Put(ctx context.Context, key string, value []byte) (int64, error) {
	resp, err := c.client.Put(ctx, &proto.PutRequest{Key: key, Value: value})
	if err != nil {
		return 0, err
	}
	return resp.Version, nil
}
