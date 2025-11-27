package acp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/magiconair/properties"
	"github.com/pingcap/go-ycsb/pkg/ycsb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	pb "github.com/rachitkumar205/acp-kv/api/proto"
)

// ycsb binding for acp key-value store
type acpDB struct {
	clients    []pb.ACPServiceClient
	conns      []*grpc.ClientConn
	mu         sync.Mutex
	nextClient int
}

type acpCreator struct{}

func init() {
	ycsb.RegisterDBCreator("acp", acpCreator{})
}

func (c acpCreator) Create(p *properties.Properties) (ycsb.DB, error) {
	// parse endpoints from properties
	endpoints := p.GetString("acp.endpoints", "localhost:8080")
	addrs := strings.Split(endpoints, ",")

	db := &acpDB{
		clients: make([]pb.ACPServiceClient, len(addrs)),
		conns:   make([]*grpc.ClientConn, len(addrs)),
	}

	// establish grpc connections to all nodes
	for i, addr := range addrs {
		addr = strings.TrimSpace(addr)

		conn, err := grpc.NewClient(
			addr,
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		)
		if err != nil {
			// cleanup already-established connections
			for j := 0; j < i; j++ {
				db.conns[j].Close()
			}
			return nil, fmt.Errorf("failed to connect to %s: %w", addr, err)
		}

		db.conns[i] = conn
		db.clients[i] = pb.NewACPServiceClient(conn)
	}

	return db, nil
}

func (db *acpDB) Close() error {
	for _, conn := range db.conns {
		if err := conn.Close(); err != nil {
			return err
		}
	}
	return nil
}

func (db *acpDB) InitThread(ctx context.Context, _ int, _ int) context.Context {
	return ctx
}

func (db *acpDB) CleanupThread(_ context.Context) {
}

// getClient returns a client using round-robin selection
func (db *acpDB) getClient() pb.ACPServiceClient {
	db.mu.Lock()
	defer db.mu.Unlock()

	client := db.clients[db.nextClient]
	db.nextClient = (db.nextClient + 1) % len(db.clients)
	return client
}

// getRowKey creates composite key from table and key
func getRowKey(table string, key string) string {
	return fmt.Sprintf("%s:%s", table, key)
}

func (db *acpDB) Read(ctx context.Context, table string, key string, fields []string) (map[string][]byte, error) {
	client := db.getClient()

	resp, err := client.Get(ctx, &pb.GetRequest{
		Key: getRowKey(table, key),
	})
	if err != nil {
		return nil, fmt.Errorf("acp get failed: %w", err)
	}

	// decode json-encoded field map
	var result map[string][]byte
	if err := json.Unmarshal(resp.Value, &result); err != nil {
		return nil, fmt.Errorf("failed to decode value: %w", err)
	}

	// filter to requested fields if specified
	if len(fields) > 0 {
		filtered := make(map[string][]byte, len(fields))
		for _, field := range fields {
			if val, ok := result[field]; ok {
				filtered[field] = val
			}
		}
		return filtered, nil
	}

	return result, nil
}

func (db *acpDB) Scan(ctx context.Context, table string, startKey string, count int, fields []string) ([]map[string][]byte, error) {
	// acp doesn't support range scans
	return nil, fmt.Errorf("scan operation not supported by acp")
}

func (db *acpDB) Update(ctx context.Context, table string, key string, values map[string][]byte) error {
	// acp doesn't distinguish between insert and update
	return db.Insert(ctx, table, key, values)
}

func (db *acpDB) Insert(ctx context.Context, table string, key string, values map[string][]byte) error {
	client := db.getClient()

	// encode field map as json
	data, err := json.Marshal(values)
	if err != nil {
		return fmt.Errorf("failed to encode values: %w", err)
	}

	// call acp put rpc
	_, err = client.Put(ctx, &pb.PutRequest{
		Key:   getRowKey(table, key),
		Value: data,
	})

	if err != nil {
		return fmt.Errorf("acp put failed: %w", err)
	}

	return nil
}

func (db *acpDB) Delete(ctx context.Context, table string, key string) error {
	// acp doesn't support delete
	return fmt.Errorf("delete operation not supported by acp")
}
