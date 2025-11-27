package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rachitkumar205/acp-kv/api/proto"
	"github.com/rachitkumar205/acp-kv/internal/adaptive"
	"github.com/rachitkumar205/acp-kv/internal/config"
	"github.com/rachitkumar205/acp-kv/internal/health"
	"github.com/rachitkumar205/acp-kv/internal/hlc"
	"github.com/rachitkumar205/acp-kv/internal/metrics"
	"github.com/rachitkumar205/acp-kv/internal/reconcile"
	"github.com/rachitkumar205/acp-kv/internal/replication"
	"github.com/rachitkumar205/acp-kv/internal/server"
	"github.com/rachitkumar205/acp-kv/internal/staleness"
	"github.com/rachitkumar205/acp-kv/internal/storage"
	"go.uber.org/zap"
	"google.golang.org/grpc"
)

func main() {
	logger, err := zap.NewProduction()

	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to initialise logger: %v\n", err)
		os.Exit(1)
	}
	defer logger.Sync()

	cfg, err := config.LoadConfig()
	if err != nil {
		logger.Fatal("failed to load configuration", zap.Error(err))
	}

	logger.Info("starting acp node",
		zap.String("node_id", cfg.NodeID),
		zap.String("listen_addr", cfg.ListenAddr),
		zap.Int("n", cfg.N),
		zap.Int("r", cfg.R),
		zap.Int("w", cfg.W),
		zap.Strings("peers", cfg.Peers))

	//validate quorum config
	if err := cfg.Validate(); err != nil {
		logger.Fatal("invalid config", zap.Error(err))
	}

	m := metrics.NewMetrics("acp")
	m.CurrentR.Set(float64(cfg.R))
	m.CurrentW.Set(float64(cfg.W))

	store := storage.NewStore()
	logger.Info("storage initialised")

	// initialize hlc clock
	hlcClock := hlc.NewClock(cfg.NodeID, cfg.HLCMaxDrift)
	logger.Info("hlc clock initialized",
		zap.String("node_id", cfg.NodeID),
		zap.Duration("max_drift", cfg.HLCMaxDrift))

	// initialize staleness detector
	stalenessDetector := staleness.NewDetector(cfg.MaxStaleness, m)
	logger.Info("staleness detector initialized",
		zap.Duration("max_staleness", cfg.MaxStaleness))

	coordinator, err := replication.NewCoordinator(cfg.NodeID, cfg.Peers, logger, m, cfg.ReplicationTimeout)
	if err != nil {
		logger.Fatal("failed to initialise replication coordinator", zap.Error(err))
	}
	defer coordinator.Close()
	logger.Info("replication coordinator initialised", zap.Int("peer_count", len(cfg.Peers)))

	probe, err := health.NewProbe(cfg.NodeID, cfg.Peers, cfg.HealthProbeInterval, logger, m)
	if err != nil {
		logger.Fatal("failed to initalise health probe", zap.Error(err))
	}
	defer probe.Stop()

	// initialize reconciliation engine
	var reconciler *reconcile.Engine
	if cfg.ReconciliationEnabled {
		reconciler = reconcile.NewEngine(
			store,
			coordinator,
			cfg.ReconciliationInterval,
			cfg.ReconciliationEnabled,
			logger,
			m,
		)
		logger.Info("reconciliation engine initialized",
			zap.Bool("enabled", cfg.ReconciliationEnabled),
			zap.Duration("interval", cfg.ReconciliationInterval))

		// set reconciler as healing listener on probe
		probe.SetHealingListener(reconciler)
		logger.Info("partition healing detection enabled")
	}

	probe.Start()
	logger.Info("health probe started")

	// create context for graceful shutdown and dynamic discovery
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// initialize quorum provider (static or adaptive)
	var quorumProvider adaptive.QuorumProvider = cfg

	if cfg.AdaptiveEnabled {
		logger.Info("initializing adaptive quorum system",
			zap.Int("initial_r", cfg.R),
			zap.Int("initial_w", cfg.W),
			zap.Int("min_r", cfg.MinR),
			zap.Int("max_r", cfg.MaxR),
			zap.Int("min_w", cfg.MinW),
			zap.Int("max_w", cfg.MaxW),
			zap.Duration("interval", cfg.AdaptiveInterval),
			zap.Float64("relax_threshold", cfg.CCSRelaxThreshold),
			zap.Float64("tighten_threshold", cfg.CCSTightenThreshold))

		// create adaptive quorum
		adaptiveQuorum := adaptive.NewAdaptiveQuorum(
			cfg.R, cfg.W, cfg.N,
			cfg.MinR, cfg.MaxR, cfg.MinW, cfg.MaxW,
			logger, m,
		)
		quorumProvider = adaptiveQuorum

		// create metrics reader
		metricsReader := metrics.NewMetricsReader(m)

		// create ccs computer
		ccsComputer := adaptive.NewCCSComputer(logger, m)

		// create and start adjuster
		adjuster := adaptive.NewAdjuster(
			adaptiveQuorum,
			metricsReader,
			coordinator,
			ccsComputer,
			cfg.AdaptiveInterval,
			cfg.CCSRelaxThreshold,
			cfg.CCSTightenThreshold,
			logger,
			m,
		)

		go adjuster.Start(ctx)
		logger.Info("adaptive quorum adjuster started")
	}

	// start reconciliation engine if enabled
	if reconciler != nil {
		go reconciler.Start(ctx)
		logger.Info("reconciliation engine started")
	}

	// start dynamic peer discovery if in kubernetes
	if headlessSvc := os.Getenv("HEADLESS_SERVICE"); headlessSvc != "" {
		namespace := getEnv("NAMESPACE", "default")
		discoveryInterval := 30 * time.Second // 30 seconds is optimal

		logger.Info("starting dynamic peer discovery",
			zap.String("method", "dns"),
			zap.String("headless_service", headlessSvc),
			zap.String("namespace", namespace),
			zap.Duration("interval", discoveryInterval))

		// start discovery for coordinator
		go coordinator.StartPeerDiscovery(ctx, cfg.NodeID, headlessSvc, namespace, discoveryInterval)

		// start discovery for health probe
		go probe.StartPeerDiscovery(ctx, cfg.NodeID, headlessSvc, namespace, discoveryInterval)
	}

	grpcServer := grpc.NewServer()
	acpServer := server.NewServer(cfg.NodeID, store, coordinator, quorumProvider, logger, m, hlcClock, stalenessDetector, reconciler)
	proto.RegisterACPServiceServer(grpcServer, acpServer)

	lis, err := net.Listen("tcp", cfg.ListenAddr)
	if err != nil {
		logger.Fatal("failed to listen", zap.String("addr", cfg.ListenAddr), zap.Error(err))
	}

	go func() {
		logger.Info("grpc server listening", zap.String("addr", cfg.ListenAddr))
		if err := grpcServer.Serve(lis); err != nil {
			logger.Fatal("grpc server failed", zap.Error(err))
		}
	}()

	//metrics http server
	http.Handle("/metrics", promhttp.Handler())
	metricsServer := &http.Server{
		Addr: cfg.MetricsAddr,
	}

	go func() {
		logger.Info("metrics server listening", zap.String("addr", cfg.MetricsAddr))
		if err := metricsServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Fatal("metrics server failed", zap.Error(err))
		}
	}()

	//wait for interrupt sig
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	<-sigCh

	logger.Info("shutting down gracefully")
	cancel() // stop discovery goroutines
	grpcServer.GracefulStop()
	metricsServer.Close()
	logger.Info("shutdown complete")
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
