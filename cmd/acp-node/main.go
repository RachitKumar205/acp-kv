package main

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rachitkumar205/acp-kv/api/proto"
	"github.com/rachitkumar205/acp-kv/internal/config"
	"github.com/rachitkumar205/acp-kv/internal/health"
	"github.com/rachitkumar205/acp-kv/internal/metrics"
	"github.com/rachitkumar205/acp-kv/internal/replication"
	"github.com/rachitkumar205/acp-kv/internal/server"
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

	logger.Info("starting ACP node",
		zap.String("node_id", cfg.NodeID),
		zap.String("listen_addr", cfg.ListenAddr),
		zap.Int("N", cfg.N),
		zap.Int("R", cfg.R),
		zap.Int("W", cfg.W),
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

	probe.Start()
	logger.Info("health probe started")

	grpcServer := grpc.NewServer()
	acpServer := server.NewServer(cfg, store, coordinator, logger, m)
	proto.RegisterACPServiceServer(grpcServer, acpServer)

	lis, err := net.Listen("tcp", cfg.ListenAddr)
	if err != nil {
		logger.Fatal("failed to listen", zap.String("addr", cfg.ListenAddr), zap.Error(err))
	}

	go func() {
		logger.Info("gRPC server listening", zap.String("addr", cfg.ListenAddr))
		if err := grpcServer.Serve(lis); err != nil {
			logger.Fatal("gRPC server failed", zap.Error(err))
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
	grpcServer.GracefulStop()
	metricsServer.Close()
	logger.Info("shutdown complete")

}
