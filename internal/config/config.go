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

	// adaptive quorum configuration
	AdaptiveEnabled      bool
	MinR                 int
	MaxR                 int
	MinW                 int
	MaxW                 int
	AdaptiveInterval     time.Duration
	CCSRelaxThreshold    float64
	CCSTightenThreshold  float64

	// hlc and staleness configuration
	HLCMaxDrift          time.Duration // maximum allowed clock drift
	MaxStaleness         time.Duration // maximum data age before rejection
	ReconciliationEnabled bool          // enable reconciliation after partition healing
	ReconciliationInterval time.Duration // interval for reconciliation checks
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

	// k8s peer discovery
	if headlessSvc := os.Getenv("HEADLESS_SERVICE"); headlessSvc != "" {
		cfg.Peers = discoverKubernetesPeers(cfg.NodeID, headlessSvc)
	} else {
		// fallback
		peersStr := getEnv("PEERS", "")
		if peersStr != "" {
			cfg.Peers = strings.Split(peersStr, ",")
			for i, peer := range cfg.Peers {
				cfg.Peers[i] = strings.TrimSpace(peer)
			}
		}
	}

	cfg.N = len(cfg.Peers) + 1

	cfg.R = getIntEnv("QUORUM_R", 2)
	cfg.W = getIntEnv("QUORUM_W", 2)

	// adaptive quorum configuration
	cfg.AdaptiveEnabled = getBoolEnv("ADAPTIVE_ENABLED", false)
	cfg.MinR = getIntEnv("MIN_R", 1)
	cfg.MaxR = getIntEnv("MAX_R", cfg.N)
	cfg.MinW = getIntEnv("MIN_W", 1)
	cfg.MaxW = getIntEnv("MAX_W", cfg.N)
	cfg.AdaptiveInterval = getDurationEnv("ADAPTIVE_INTERVAL", 2*time.Second)
	cfg.CCSRelaxThreshold = getFloatEnv("CCS_RELAX_THRESHOLD", 0.45)
	cfg.CCSTightenThreshold = getFloatEnv("CCS_TIGHTEN_THRESHOLD", 0.75)

	// hlc and staleness configuration
	cfg.HLCMaxDrift = getDurationEnv("HLC_MAX_DRIFT", 500*time.Millisecond)
	cfg.MaxStaleness = getDurationEnv("MAX_STALENESS", 3*time.Second)
	cfg.ReconciliationEnabled = getBoolEnv("RECONCILIATION_ENABLED", false)
	cfg.ReconciliationInterval = getDurationEnv("RECONCILIATION_INTERVAL", 30*time.Second)

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

func discoverKubernetesPeers(nodeID, headlessSvc string) []string {
	clusterSize := getIntEnv("CLUSTER_SIZE", 3)
	namespace := getEnv("NAMESPACE", "default")

	peers := []string{}
	for i := 0; i < clusterSize; i++ {
		peerName := fmt.Sprintf("acp-node-%d", i)

		if peerName != nodeID {
			peerAddr := fmt.Sprintf("%s.%s.%s.svc.cluster.local:8080",
				peerName, headlessSvc, namespace)
			peers = append(peers, peerAddr)
		}
	}

	return peers
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

func getBoolEnv(key string, defaultValue bool) bool {
	if value := os.Getenv(key); value != "" {
		if boolVal, err := strconv.ParseBool(value); err == nil {
			return boolVal
		}
	}

	return defaultValue
}

func getFloatEnv(key string, defaultValue float64) float64 {
	if value := os.Getenv(key); value != "" {
		if floatVal, err := strconv.ParseFloat(value, 64); err == nil {
			return floatVal
		}
	}

	return defaultValue
}

// implement quorumprovider interface (for static mode)
func (c *Config) GetR() int {
	return c.R
}

func (c *Config) GetW() int {
	return c.W
}

func (c *Config) GetN() int {
	return c.N
}
