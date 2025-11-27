package adaptive

import (
	"context"
	"time"

	"github.com/rachitkumar205/acp-kv/internal/metrics"
	"github.com/rachitkumar205/acp-kv/internal/replication"
	"go.uber.org/zap"
)

// adjuster implements the closed-loop feedback controller
type Adjuster struct {
	quorum        *AdaptiveQuorum
	metricsReader *metrics.MetricsReader
	coordinator   CoordinatorInterface
	ccsComputer   *CCSComputer
	interval      time.Duration
	logger        *zap.Logger
	metrics       *metrics.Metrics

	// thresholds for adjustment
	relaxThreshold  float64 // ccs < 0.45 triggers relax (decrease w)
	tightenThreshold float64 // ccs > 0.75 triggers tighten (increase w)
}

// coordinatorinterface defines methods needed from coordinator
type CoordinatorInterface interface {
	GetPeerAddresses() []string
}

// newadjuster creates a new adaptive quorum adjuster
func NewAdjuster(
	quorum *AdaptiveQuorum,
	metricsReader *metrics.MetricsReader,
	coordinator CoordinatorInterface,
	ccsComputer *CCSComputer,
	interval time.Duration,
	relaxThreshold float64,
	tightenThreshold float64,
	logger *zap.Logger,
	m *metrics.Metrics,
) *Adjuster {
	return &Adjuster{
		quorum:           quorum,
		metricsReader:    metricsReader,
		coordinator:      coordinator,
		ccsComputer:      ccsComputer,
		interval:         interval,
		relaxThreshold:   relaxThreshold,
		tightenThreshold: tightenThreshold,
		logger:           logger,
		metrics:          m,
	}
}

// start runs the adjuster control loop
func (a *Adjuster) Start(ctx context.Context) {
	ticker := time.NewTicker(a.interval)
	defer ticker.Stop()

	a.logger.Info("adaptive quorum adjuster starting",
		zap.Duration("interval", a.interval),
		zap.Float64("relax_threshold", a.relaxThreshold),
		zap.Float64("tighten_threshold", a.tightenThreshold))

	for {
		select {
		case <-ticker.C:
			a.adjustQuorum()
		case <-ctx.Done():
			a.logger.Info("adaptive quorum adjuster stopped")
			return
		}
	}
}

// adjustquorum performs a single adjustment cycle
func (a *Adjuster) adjustQuorum() {
	// 1. gather metrics from prometheus registry
	peers := a.coordinator.GetPeerAddresses()

	// get write success rate
	successRate := a.metricsReader.GetWriteSuccessRate()

	// get peer latency stats AND count reachable peers
	latencyStats, err := a.metricsReader.GetAllPeersLatencyStats(peers)
	if err != nil {
		a.logger.Warn("failed to get latency stats", zap.Error(err))
		return
	}

	// calculate peer availability (fraction of peers reachable)
	// if we can't get stats from a peer, it's down
	peerAvailability := float64(latencyStats.Count) / float64(len(peers))

	// combine write success rate with peer availability
	// both must be high for overall availability to be high
	combinedAvailability := successRate * peerAvailability

	// calculate error rate (inverse of combined availability)
	errorRate := 1.0 - combinedAvailability

	// get variance from latency stats (using p95 - avg as proxy for variance)
	variance := 0.0
	if latencyStats.Count > 0 && latencyStats.P95 > 0 {
		// approximate variance using spread between p95 and average
		spread := latencyStats.P95 - latencyStats.Avg
		variance = spread * spread
	}

	// record per-peer variance in ms^2
	for _, peer := range peers {
		peerStats, err := a.metricsReader.GetPeerLatencyStats(peer)
		if err != nil {
			continue
		}
		if peerStats.Count > 0 && peerStats.P95 > 0 {
			peerSpread := peerStats.P95 - peerStats.Avg
			peerVarianceMs2 := (peerSpread * 1000) * (peerSpread * 1000) // convert to ms^2
			a.metrics.RTTVariance.WithLabelValues(peer).Set(peerVarianceMs2)
		}
	}

	// get clock drift stats (average across all peers)
	clockDrift := a.metricsReader.GetClockDriftStats(peers)

	// 2. record metrics and compute ccs
	a.ccsComputer.RecordMetrics(latencyStats.Avg, combinedAvailability, variance, errorRate, clockDrift)

	rawCCS, components := a.ccsComputer.ComputeCCS()
	a.ccsComputer.AddToCCSHistory(rawCCS)
	smoothedCCS := a.ccsComputer.GetSmoothedCCS()

	// 3. update prometheus gauges
	a.ccsComputer.UpdateMetricsGauges(rawCCS, smoothedCCS, components)

	// update hysteresis gauge
	if a.quorum.IsInLockout() {
		a.metrics.HysteresisActive.Set(1)
	} else {
		a.metrics.HysteresisActive.Set(0)
	}

	// 4. log current state
	currentR := a.quorum.GetR()
	currentW := a.quorum.GetW()

	a.logger.Info("ccs computed",
		zap.Float64("raw_ccs", rawCCS),
		zap.Float64("smoothed_ccs", smoothedCCS),
		zap.Float64("rtt_health", components.RTTHealth),
		zap.Float64("avail_health", components.AvailHealth),
		zap.Float64("var_health", components.VarHealth),
		zap.Float64("error_health", components.ErrorHealth),
		zap.Float64("clock_health", components.ClockHealth),
		zap.Float64("avg_latency_ms", latencyStats.Avg*1000),
		zap.Float64("p95_latency_ms", latencyStats.P95*1000),
		zap.Float64("clock_drift_ms", clockDrift*1000),
		zap.Float64("success_rate", successRate),
		zap.Int("current_r", currentR),
		zap.Int("current_w", currentW),
		zap.Int("peer_count", len(peers)),
		zap.Int("reachable_peers", int(latencyStats.Count)))

	// 5. check hysteresis lockout
	if a.quorum.IsInLockout() {
		a.logger.Debug("skipping adjustment: in hysteresis lockout period")
		return
	}

	// 6. evaluate thresholds and decide adjustment
	var newR, newW int
	var reason string
	var shouldAdjust bool

	if smoothedCCS < a.relaxThreshold {
		// cluster unhealthy - relax consistency (decrease w, increase r)
		newW = currentW - 1
		newR = currentR + 1
		reason = "relax"
		shouldAdjust = true

		a.logger.Info("ccs below relax threshold",
			zap.Float64("smoothed_ccs", smoothedCCS),
			zap.Float64("threshold", a.relaxThreshold))

	} else if smoothedCCS > a.tightenThreshold {
		// cluster healthy - tighten consistency (increase w, decrease r)
		newW = currentW + 1
		newR = currentR - 1
		reason = "tighten"
		shouldAdjust = true

		a.logger.Info("ccs above tighten threshold",
			zap.Float64("smoothed_ccs", smoothedCCS),
			zap.Float64("threshold", a.tightenThreshold))
	} else {
		// ccs in stable region - no adjustment needed
		a.logger.Debug("ccs in stable region, no adjustment needed",
			zap.Float64("smoothed_ccs", smoothedCCS))
		return
	}

	if !shouldAdjust {
		return
	}

	// 7. validate adjustment
	if err := a.quorum.Validate(newR, newW); err != nil {
		a.logger.Warn("adjustment rejected by validation",
			zap.Int("attempted_r", newR),
			zap.Int("attempted_w", newW),
			zap.String("reason", reason),
			zap.Error(err))
		return
	}

	// 8. apply adjustment
	if err := a.quorum.SetQuorum(newR, newW, reason); err != nil {
		a.logger.Error("failed to apply quorum adjustment",
			zap.Int("attempted_r", newR),
			zap.Int("attempted_w", newW),
			zap.String("reason", reason),
			zap.Error(err))
		return
	}

	// 9. record adjustment in prometheus
	a.metrics.QuorumAdjustments.Inc()
	a.metrics.QuorumAdjustmentReason.WithLabelValues(reason).Inc()

	a.logger.Info("quorum adjustment applied",
		zap.Int("old_r", currentR),
		zap.Int("new_r", newR),
		zap.Int("old_w", currentW),
		zap.Int("new_w", newW),
		zap.String("reason", reason),
		zap.Float64("smoothed_ccs", smoothedCCS),
		zap.Float64("raw_ccs", rawCCS))
}

// ensure coordinator implements coordinatorinterface
var _ CoordinatorInterface = (*replication.Coordinator)(nil)
