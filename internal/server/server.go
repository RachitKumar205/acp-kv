package server

import (
	"context"
	"time"

	"github.com/rachitkumar205/acp-kv/api/proto"
	"github.com/rachitkumar205/acp-kv/internal/adaptive"
	"github.com/rachitkumar205/acp-kv/internal/hlc"
	"github.com/rachitkumar205/acp-kv/internal/metrics"
	"github.com/rachitkumar205/acp-kv/internal/reconcile"
	"github.com/rachitkumar205/acp-kv/internal/replication"
	"github.com/rachitkumar205/acp-kv/internal/staleness"
	"github.com/rachitkumar205/acp-kv/internal/storage"
	"go.uber.org/zap"
)

type Server struct {
	proto.UnimplementedACPServiceServer
	nodeID            string
	store             *storage.Store
	coordinator       *replication.Coordinator
	quorumProvider    adaptive.QuorumProvider
	logger            *zap.Logger
	metrics           *metrics.Metrics
	hlcClock          *hlc.Clock            // hybrid logical clock
	stalenessDetector *staleness.Detector   // staleness enforcement
	reconciler        *reconcile.Engine     // reconciliation engine (optional)
}

func NewServer(
	nodeID string,
	store *storage.Store,
	coordinator *replication.Coordinator,
	quorumProvider adaptive.QuorumProvider,
	logger *zap.Logger,
	metrics *metrics.Metrics,
	hlcClock *hlc.Clock,
	stalenessDetector *staleness.Detector,
	reconciler *reconcile.Engine,
) *Server {
	return &Server{
		nodeID:            nodeID,
		store:             store,
		coordinator:       coordinator,
		quorumProvider:    quorumProvider,
		logger:            logger,
		metrics:           metrics,
		hlcClock:          hlcClock,
		stalenessDetector: stalenessDetector,
		reconciler:        reconciler,
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

	// generate hlc timestamp for this write
	timestamp := s.hlcClock.Now()

	// write to local store with hlc timestamp
	vv := s.store.PutWithHLC(req.Key, req.Value, s.nodeID, timestamp)

	// record write in reconciliation log
	if s.reconciler != nil {
		s.reconciler.RecordWrite(req.Key, req.Value, s.nodeID, timestamp)
	}

	// get current write quorum size
	requiredW := s.quorumProvider.GetW()

	// replicate to peers and wait for W acks
	acks, _, err := s.coordinator.Replicate(ctx, req.Key, req.Value, vv.Version, vv.Timestamp, timestamp, requiredW)

	if err != nil {
		s.logger.Error("PUT failed - insufficient acks",
			zap.String("key", req.Key),
			zap.Int("acks", acks),
			zap.Int("required", requiredW),
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
		Hlc:       vv.HLC.ToProto(),
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

	// get current read quorum size
	requiredR := s.quorumProvider.GetR()

	// if R = 1, return local value immediately
	if requiredR == 1 {
		if !localFound {
			s.logger.Info("GET not found (local only)", zap.String("key", req.Key))
			s.metrics.RecordReadSuccess()
			return &proto.GetResponse{Found: false}, nil
		}

		// check staleness in strict mode
		if err := s.stalenessDetector.CheckStrict(localValue); err != nil {
			s.logger.Warn("GET rejected - staleness bound exceeded",
				zap.String("key", req.Key),
				zap.Error(err))
			s.metrics.RecordReadFailure()
			return &proto.GetResponse{
				Found:   true,
				IsStale: true,
				Error:   err.Error(),
			}, nil
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
			Hlc:       localValue.HLC.ToProto(),
			IsStale:   false,
		}, nil
	}

	// query R-1 replicas
	replicaValues, err := s.coordinator.QueryReplicas(ctx, req.Key, requiredR)
	if err != nil {
		s.logger.Error("GET failed - insufficient responses",
			zap.String("key", req.Key),
			zap.Int("required", requiredR),
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
			HLC:       localValue.HLC,
			IsStale:   false,
			Found:     true,
		})
	}

	mostRecent, found := replication.GetMostRecent(allValues)
	if !found {
		s.logger.Info("GET not found (quorum) read", zap.String("key", req.Key))
		s.metrics.RecordReadSuccess()
		return &proto.GetResponse{Found: false}, nil
	}

	// check staleness of most recent value in strict mode
	mostRecentVV := storage.VersionedValue{
		Value:   mostRecent.Value,
		Version: mostRecent.Version,
		HLC:     mostRecent.HLC,
		NodeID:  s.nodeID,
	}
	if err := s.stalenessDetector.CheckStrict(mostRecentVV); err != nil {
		s.logger.Warn("GET rejected - staleness bound exceeded (quorum)",
			zap.String("key", req.Key),
			zap.String("source", mostRecent.PeerAddr),
			zap.Error(err))
		s.metrics.RecordReadFailure()
		return &proto.GetResponse{
			Found:   true,
			IsStale: true,
			Error:   err.Error(),
		}, nil
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
		Hlc:       mostRecent.HLC.ToProto(),
		IsStale:   false,
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

	// check staleness (for read repair decision)
	now := time.Now().UnixNano()
	isStale := s.stalenessDetector.IsStale(localValue.HLC, now)

	return &proto.GetResponse{
		Found:     true,
		Value:     localValue.Value,
		Version:   localValue.Version,
		Timestamp: localValue.Timestamp,
		Hlc:       localValue.HLC.ToProto(),
		IsStale:   isStale,
	}, nil
}

// handle replication requests from other other nodes
func (s *Server) Replicate(ctx context.Context, req *proto.ReplicateRequest) (*proto.ReplicateResponse, error) {
	s.logger.Debug("REPLICATE request received",
		zap.String("key", req.Key),
		zap.String("source", req.SourceNodeId),
		zap.Int64("version", req.Version))

	// extract hlc timestamp from request
	remoteHLC := hlc.FromProto(req.Hlc)

	// update local clock with remote timestamp (clock sync)
	if err := s.hlcClock.Update(remoteHLC); err != nil {
		s.logger.Warn("clock update failed during replication",
			zap.String("source", req.SourceNodeId),
			zap.Error(err))
		// continue with replication despite clock drift warning
	}

	// store with hlc timestamp
	s.store.PutWithHLC(req.Key, req.Value, req.SourceNodeId, remoteHLC)

	// record replicated write in reconciliation log
	if s.reconciler != nil {
		s.reconciler.RecordWrite(req.Key, req.Value, req.SourceNodeId, remoteHLC)
	}

	return &proto.ReplicateResponse{
		Success: true,
		NodeId:  s.nodeID,
	}, nil
}

// handle health check requests
func (s *Server) HealthCheck(ctx context.Context, req *proto.HealthRequest) (*proto.HealthResponse, error) {
	// update clock with remote timestamp if provided
	if req.Hlc != nil {
		remoteHLC := hlc.FromProto(req.Hlc)
		if err := s.hlcClock.Update(remoteHLC); err != nil {
			s.logger.Debug("clock update failed during health check",
				zap.String("source", req.SourceNodeId),
				zap.Error(err))
		}
	}

	// generate current hlc timestamp
	currentHLC := s.hlcClock.Now()

	return &proto.HealthResponse{
		Healthy:   true,
		NodeId:    s.nodeID,
		Timestamp: time.Now().UnixNano(),
		Hlc:       currentHLC.ToProto(),
	}, nil
}
