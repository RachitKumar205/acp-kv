package server

import (
	"context"
	"time"

	"github.com/rachitkumar205/acp-kv/api/proto"
	"github.com/rachitkumar205/acp-kv/internal/config"
	"github.com/rachitkumar205/acp-kv/internal/metrics"
	"github.com/rachitkumar205/acp-kv/internal/replication"
	"github.com/rachitkumar205/acp-kv/internal/storage"
	"go.uber.org/zap"
)

type Server struct {
	proto.UnimplementedACPServiceServer
	nodeID      string
	store       *storage.Store
	coordinator *replication.Coordinator
	config      *config.Config
	logger      *zap.Logger
	metrics     *metrics.Metrics
}

func NewServer(cfg *config.Config, store *storage.Store, coordinator *replication.Coordinator, logger *zap.Logger, metrics *metrics.Metrics) *Server {
	return &Server{
		nodeID:      cfg.NodeID,
		store:       store,
		coordinator: coordinator,
		config:      cfg,
		logger:      logger,
		metrics:     metrics,
	}
}

// handle client write requests with quorum replication
func (s *Server) Put(ctx context.Context, req *proto.PutRequest) (*proto.PutResponse, error) {
	start := time.Now()
	defer func() {
		s.metrics.PutLatency.Observe(time.Since(start).Seconds())
	}()

	s.logger.Info("PUT request received",
		zap.String("key", req.Key),
		zap.Int("value_size", len(req.Value)))

	// write to local store first
	vv := s.store.Put(req.Key, req.Value, s.nodeID)

	// replicate to peers and wait for W acks
	acks, _, err := s.coordinator.Replicate(ctx, req.Key, req.Value, vv.Version, vv.Timestamp, s.config.W)

	if err != nil {
		s.logger.Error("PUT failed - insufficient acks",
			zap.String("key", req.Key),
			zap.Int("acks", acks),
			zap.Int("required", s.config.W),
			zap.Error(err))
		s.metrics.RecordWriteFailure()
		s.metrics.Errors.WithLabelValues("timeout").Inc()
		return &proto.PutResponse{
			Success: false,
			Error:   err.Error(),
		}, nil
	}

	s.logger.Info("PUT succeeded",
		zap.String("key", req.Key),
		zap.Int("acks", acks),
		zap.Int64("version", vv.Version),
		zap.Duration("latency", time.Since(start)))

	s.metrics.RecordWriteSuccess()

	return &proto.PutResponse{
		Success:   true,
		Version:   vv.Version,
		Timestamp: vv.Timestamp,
	}, nil
}

// handle client read requests with quorum reads
func (s *Server) Get(ctx context.Context, req *proto.GetRequest) (*proto.GetResponse, error) {
	start := time.Now()
	defer func() {
		s.metrics.GetLatency.Observe(time.Since(start).Seconds())
	}()

	s.logger.Info("GET request received", zap.String("key", req.Key))

	//query local store
	localValue, localFound := s.store.Get(req.Key)

	// if R = 1, return local value immediately
	if s.config.R == 1 {
		if !localFound {
			s.logger.Info("GET not found (local only)", zap.String("key", req.Key))
			s.metrics.RecordReadSuccess()
			return &proto.GetResponse{Found: false}, nil
		}

		s.logger.Info("GET succeeded (local)",
			zap.String("key", req.Key),
			zap.Int64("version", localValue.Version))
		s.metrics.RecordReadSuccess()

		return &proto.GetResponse{
			Found:     true,
			Value:     localValue.Value,
			Version:   localValue.Version,
			Timestamp: localValue.Timestamp,
		}, nil
	}

	// query R-1 replicas
	replicaValues, err := s.coordinator.QueryReplicas(ctx, req.Key, s.config.R)
	if err != nil {
		s.logger.Error("GET failed - insufficient responses",
			zap.String("key", req.Key),
			zap.Int("required", s.config.R),
			zap.Error(err))
		s.metrics.RecordReadFailure()
		s.metrics.Errors.WithLabelValues("timeout").Inc()
		return &proto.GetResponse{
			Found: false,
			Error: err.Error(),
		}, nil
	}

	allValues := replicaValues
	if localFound {
		allValues = append(allValues, replication.ReplicaValue{
			PeerAddr:  "local",
			Value:     localValue.Value,
			Version:   localValue.Version,
			Timestamp: localValue.Timestamp,
			Found:     true,
		})
	}

	mostRecent, found := replication.GetMostRecent(allValues)
	if !found {
		s.logger.Info("GET not found (quorum) read", zap.String("key", req.Key))
		s.metrics.RecordReadSuccess()
		return &proto.GetResponse{Found: false}, nil
	}

	s.logger.Info("GET succeeded (quorum read)",
		zap.String("key", req.Key),
		zap.Int64("version", mostRecent.Version),
		zap.String("source", mostRecent.PeerAddr),
		zap.Duration("latency", time.Since(start)))

	s.metrics.RecordReadSuccess()

	return &proto.GetResponse{
		Found:     true,
		Value:     mostRecent.Value,
		Version:   mostRecent.Version,
		Timestamp: mostRecent.Timestamp,
	}, nil

}

// handle local-only get requests from peer nodes during quorum reads
func (s *Server) GetLocal(ctx context.Context, req *proto.GetRequest) (*proto.GetResponse, error) {
	s.logger.Info("GET LOCAL request received", zap.String("key", req.Key))

	// only query local store, no quorum
	localValue, localFound := s.store.Get(req.Key)

	if !localFound {
		return &proto.GetResponse{Found: false}, nil
	}

	return &proto.GetResponse{
		Found:     true,
		Value:     localValue.Value,
		Version:   localValue.Version,
		Timestamp: localValue.Timestamp,
	}, nil
}

// handle replication requests from other other nodes
func (s *Server) Replicate(ctx context.Context, req *proto.ReplicateRequest) (*proto.ReplicateResponse, error) {
	s.logger.Debug("REPLICATE request received",
		zap.String("key", req.Key),
		zap.String("source", req.SourceNodeId),
		zap.Int64("version", req.Version))

	s.store.Put(req.Key, req.Value, req.SourceNodeId)

	return &proto.ReplicateResponse{
		Success: true,
		NodeId:  s.nodeID,
	}, nil
}

// handle health check requests
func (s *Server) HealthCheck(ctx context.Context, req *proto.HealthRequest) (*proto.HealthResponse, error) {
	return &proto.HealthResponse{
		Healthy:   true,
		NodeId:    s.nodeID,
		Timestamp: time.Now().UnixNano(),
	}, nil
}
