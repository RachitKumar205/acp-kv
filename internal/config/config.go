package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// configuration for an acp node
type Config struct {
	NodeID     string
	ListenAddr string

	// cluster config
	Peers []string // list of peer addresses
	N     int      // total no. of nodes

	// quorum params
	R int
	W int

	// timeouts
	ReplicationTimeout  time.Duration
	HealthProbeInterval time.Duration

	// metrics
	MetricsAddr string
}

// load config from env vars
func LoadConfig() (*Config, error) {
	cfg := &Config{
		NodeID:              getEnv("NODE_ID", "node1"),
		ListenAddr:          getEnv("LISTEN_ADDR", ":8080"),
		MetricsAddr:         getEnv("METRICS_ADDR", ":9090"),
		ReplicationTimeout:  getDurationEnv("REPLICATION_TIMEOUT", 500*time.Millisecond),
		HealthProbeInterval: getDurationEnv("HEALTH_PROBE_INTERVAL", 500*time.Millisecond),
	}

	// parse peers
	peersStr := getEnv("PEERS", "")
	if peersStr != "" {
		cfg.Peers = strings.Split(peersStr, ",")
		for i, peer := range cfg.Peers {
			cfg.Peers[i] = strings.TrimSpace(peer)
		}
	}

	cfg.N = len(cfg.Peers) + 1

	// parse quorum params
	cfg.R = getIntEnv("QUORUM_R", 2)
	cfg.W = getIntEnv("QUORUM_W", 2)

	// valdate
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

// validation checks for config
func (c *Config) Validate() error {
	if c.NodeID == "" {
		return errors.New("NODE_ID cannot be empty")
	}

	if c.N < 3 {
		return fmt.Errorf("cluster must have atleast 3 nodes, got %d", c.N)
	}

	if c.R < 1 || c.R > c.N {
		return fmt.Errorf("R must be between 1 and %d, got %d", c.N, c.R)
	}

	if c.W < 1 || c.W > c.N {
		return fmt.Errorf("W must be between 1 and %d, got %d", c.N, c.W)
	}

	// validate quorum intersection ( R + W > N )
	if c.R+c.W <= c.N {
		return fmt.Errorf("quorum intersection violated")
	}

	return nil
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}

	return defaultValue
}

func getIntEnv(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if intVal, err := strconv.Atoi(value); err == nil {
			return intVal
		}
	}

	return defaultValue
}

func getDurationEnv(key string, defaultValue time.Duration) time.Duration {
	if value := os.Getenv(key); value != "" {
		if duration, err := time.ParseDuration(value); err == nil {
			return duration
		}
	}

	return defaultValue
}
