package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/rachitkumar205/acp-kv/pkg/client"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Println("Usage:")
		fmt.Println("	acp-cli <address> put <key> <value>")
		fmt.Println("	acp-cli <address> get <key>")
		fmt.Println("	acp-cli <address> health")
		os.Exit(1)
	}

	addr := os.Args[1]
	cmd := os.Args[2]

	c, err := client.NewClient(addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error connecting: %v\n", err)
		os.Exit(1)
	}
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	switch cmd {
	case "put":
		if len(os.Args) < 5 {
			fmt.Println("Usage: acp-cli <address> put <key> <value>")
			os.Exit(1)
		}
		key := os.Args[3]
		value := os.Args[4]

		resp, err := c.Put(ctx, key, []byte(value))
		if err != nil {
			fmt.Fprintf(os.Stderr, "PUT failed: %v\n", err)
			os.Exit(1)
		}

		if resp.Success {
			fmt.Printf("put successful\n")
			fmt.Printf("version: %d\n", resp.Version)
			fmt.Printf("timestamp: %d\n", resp.Timestamp)
		} else {
			fmt.Printf("put failed: %s\n", resp.Error)
			os.Exit(1)
		}

	case "get":
		if len(os.Args) < 4 {
			fmt.Println("Usage: acp-cli <address> get <key>")
			os.Exit(1)
		}
		key := os.Args[3]

		resp, err := c.Get(ctx, key)
		if err != nil {
			fmt.Fprintf(os.Stderr, "GET failed: %v\n", err)
			os.Exit(1)
		}

		if resp.Found {
			fmt.Printf("key found\n")
			fmt.Printf("value: %s\n", string(resp.Value))
			fmt.Printf("version: %d\n", resp.Version)
			fmt.Printf("timestamp: %d\n", resp.Timestamp)
		} else {
			fmt.Printf("key not found\n")
			if resp.Error != "" {
				fmt.Printf("error: %s\n", resp.Error)
			}
			os.Exit(1)
		}

	case "health":
		resp, err := c.HealthCheck(ctx, "cli")
		if err != nil {
			fmt.Fprintf(os.Stderr, "health check failed: %v\n", err)
			os.Exit(1)
		}

		if resp.Healthy {
			fmt.Printf("node healthy\n")
			fmt.Printf("node ID: %s\n", resp.NodeId)
			fmt.Printf("timestamp: %d\n", resp.Timestamp)
		} else {
			fmt.Printf("node unhealthy\n")
			os.Exit(1)
		}

	default:
		fmt.Printf("unknown command: %s\n", cmd)
		fmt.Println("valid commands: put, get, health")
		os.Exit(1)

	}
}
