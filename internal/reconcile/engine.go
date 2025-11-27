package reconcile

import (
	"context"
	"sync"
	"time"

	"github.com/rachitkumar205/acp-kv/internal/hlc"
	"github.com/rachitkumar205/acp-kv/internal/metrics"
	"github.com/rachitkumar205/acp-kv/internal/storage"
	"go.uber.org/zap"
)

// engine performs anti-entropy reconciliation after partition healing
type Engine struct {
	store         *storage.Store
	recentWrites  *RecentWriteLog
	coordinator   ReconcilerCoordinator
	logger        *zap.Logger
	metrics       *metrics.Metrics
	interval      time.Duration
	enabled       bool
	mu            sync.RWMutex
	healingEvents chan string // peer addresses that just healed
}

// reconcilercoordinator defines methods needed from coordinator
type ReconcilerCoordinator interface {
	GetPeerAddresses() []string
}

// newengine creates a new reconciliation engine
func NewEngine(
	store *storage.Store,
	coordinator ReconcilerCoordinator,
	interval time.Duration,
	enabled bool,
	logger *zap.Logger,
	m *metrics.Metrics,
) *Engine {
	return &Engine{
		store:         store,
		recentWrites:  NewRecentWriteLog(1000, 5*time.Minute), // keep 1000 writes for 5 minutes
		coordinator:   coordinator,
		logger:        logger,
		metrics:       m,
		interval:      interval,
		enabled:       enabled,
		healingEvents: make(chan string, 100),
	}
}

// start runs the reconciliation engine
func (e *Engine) Start(ctx context.Context) {
	if !e.enabled {
		e.logger.Info("reconciliation engine disabled")
		return
	}

	e.logger.Info("reconciliation engine starting",
		zap.Duration("interval", e.interval))

	ticker := time.NewTicker(e.interval)
	defer ticker.Stop()

	for {
		select {
		case peer := <-e.healingEvents:
			e.logger.Info("partition healing detected, triggering reconciliation",
				zap.String("healed_peer", peer))
			e.metrics.PartitionHealing.Inc()
			e.reconcileWithPeer(peer)

		case <-ticker.C:
			// periodic reconciliation check (optional)
			e.logger.Debug("periodic reconciliation check")

		case <-ctx.Done():
			e.logger.Info("reconciliation engine stopped")
			return
		}
	}
}

// notifyhealingevent signals that a peer has reconnected
func (e *Engine) NotifyHealingEvent(peer string) {
	select {
	case e.healingEvents <- peer:
		e.logger.Debug("healing event queued", zap.String("peer", peer))
	default:
		e.logger.Warn("healing event queue full, dropping event",
			zap.String("peer", peer))
	}
}

// reconcile with a specific peer using recent write log
func (e *Engine) reconcileWithPeer(peer string) {
	start := time.Now()
	defer func() {
		e.metrics.ReconciliationLatency.Observe(time.Since(start).Seconds())
	}()

	e.logger.Info("starting reconciliation",
		zap.String("peer", peer))

	// get recent writes
	writes := e.recentWrites.GetAll()
	keysReconciled := 0

	for _, write := range writes {
		// for each recent write, check local store and resolve conflicts
		localValue, found := e.store.Get(write.Key)

		if !found {
			// key doesn't exist locally, skip
			continue
		}

		// use lww: keep the value with the latest hlc timestamp
		if write.HLC.HappensAfter(localValue.HLC) {
			// remote write is newer, update local store
			e.store.PutWithHLC(write.Key, write.Value, write.NodeID, write.HLC)
			keysReconciled++
			e.metrics.ConflictsResolved.Inc()
			e.logger.Debug("reconciliation: remote write newer",
				zap.String("key", write.Key),
				zap.String("peer", peer))
		} else if localValue.HLC.HappensAfter(write.HLC) {
			// local value is newer, no action needed
			e.logger.Debug("reconciliation: local value newer",
				zap.String("key", write.Key))
		} else if write.HLC.Equal(localValue.HLC) {
			// concurrent writes with same hlc (should be rare)
			e.metrics.ConflictsDetected.Inc()
			e.logger.Warn("reconciliation: concurrent writes detected",
				zap.String("key", write.Key),
				zap.String("local_node", localValue.NodeID),
				zap.String("remote_node", write.NodeID))

			// tiebreak using node id (deterministic)
			if write.NodeID > localValue.NodeID {
				e.store.PutWithHLC(write.Key, write.Value, write.NodeID, write.HLC)
				keysReconciled++
				e.metrics.ConflictsResolved.Inc()
			}
		}
	}

	e.metrics.ReconciliationRuns.Inc()
	e.metrics.ReconciliationKeys.Observe(float64(keysReconciled))

	e.logger.Info("reconciliation completed",
		zap.String("peer", peer),
		zap.Int("keys_reconciled", keysReconciled),
		zap.Int("total_writes_checked", len(writes)),
		zap.Duration("duration", time.Since(start)))
}

// recordwrite adds a write to the recent write log
func (e *Engine) RecordWrite(key string, value []byte, nodeID string, timestamp hlc.HLC) {
	e.recentWrites.Add(key, value, nodeID, timestamp)
}
