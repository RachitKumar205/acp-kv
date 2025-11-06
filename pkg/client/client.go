package client

import (
	"context"
	"fmt"
	"time"

	"github.com/rachitkumar205/acp-kv/api/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type Client struct {
	conn   *grpc.ClientConn
	client proto.ACPServiceClient
}

func NewClient(addr string) (*Client, error) {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("failed to connect: %w", err)
	}

	return &Client{
		conn:   conn,
		client: proto.NewACPServiceClient(conn),
	}, nil
}

func (c *Client) Close() error {
	return c.conn.Close()
}

func (c *Client) Put(ctx context.Context, key string, value []byte) (*proto.PutResponse, error) {
	return c.client.Put(ctx, &proto.PutRequest{
		Key:   key,
		Value: value,
	})
}

func (c *Client) Get(ctx context.Context, key string) (*proto.GetResponse, error) {
	return c.client.Get(ctx, &proto.GetRequest{
		Key: key,
	})
}

func (c *Client) HealthCheck(ctx context.Context, sourceNodeID string) (*proto.HealthResponse, error) {
	return c.client.HealthCheck(ctx, &proto.HealthRequest{
		SourceNodeId: sourceNodeID,
		Timestamp:    time.Now().UnixNano(),
	})
}
