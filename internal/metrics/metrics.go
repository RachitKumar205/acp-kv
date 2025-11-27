package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// holds all prometheus metrics
type Metrics struct {
	// latency histogram
	PutLatency       prometheus.Histogram
	GetLatency       prometheus.Histogram
	ReplicateLatency *prometheus.HistogramVec

	// success/failure counters
	ReplicateAcks *prometheus.CounterVec
	Errors        *prometheus.CounterVec

	// success ratios
	WriteSuccessTotal prometheus.Counter
	WriteFailureTotal prometheus.Counter
	ReadSuccessTotal  prometheus.Counter
	ReadFailureTotal  prometheus.Counter

	// quorum gauges
	CurrentR prometheus.Gauge
	CurrentW prometheus.Gauge

	// health metrics
	HealthRTT         *prometheus.GaugeVec
	RTTVariance       *prometheus.GaugeVec // RTT variance per peer in ms^2

	// throughput metrics
	WriteOpsTotal prometheus.Counter // total write operations (acp_write_ops_total)

	// adaptive quorum metrics
	CCSRaw               prometheus.Gauge
	CCSSmoothed          prometheus.Gauge
	CCSComponentRTT      prometheus.Gauge
	CCSComponentAvail    prometheus.Gauge
	CCSComponentVar      prometheus.Gauge
	CCSComponentError    prometheus.Gauge
	CCSComponentClock    prometheus.Gauge
	QuorumAdjustments    prometheus.Counter
	QuorumAdjustmentReason *prometheus.CounterVec
	HysteresisActive     prometheus.Gauge

	// hlc and staleness metrics
	HLCDrift            *prometheus.GaugeVec // drift per peer in milliseconds
	StalenessViolations prometheus.Counter   // total staleness bound violations
	StaleReadsRejected  prometheus.Counter   // total reads rejected due to staleness
	DataAge             prometheus.Histogram  // distribution of data age on reads

	// conflict and reconciliation metrics
	ConflictsDetected     prometheus.Counter    // total conflicts detected
	ConflictsResolved     prometheus.Counter    // total conflicts resolved (lww)
	ReconciliationRuns    prometheus.Counter    // total reconciliation runs
	ReconciliationKeys    prometheus.Histogram  // keys reconciled per run
	ReconciliationLatency prometheus.Histogram  // reconciliation duration
	PartitionHealing      prometheus.Counter    // partition healing events detected
	ReadRepair            prometheus.Counter    // read repair operations
}

// create and register all prometheus metrics
func NewMetrics(namespace string) *Metrics {
	m := &Metrics{
		PutLatency: promauto.NewHistogram(prometheus.HistogramOpts{
			Namespace: namespace,
			Name:      "put_latency_seconds",
			Help:      "Latency of PUT operations",
			Buckets:   prometheus.DefBuckets,
		}),

		GetLatency: promauto.NewHistogram(prometheus.HistogramOpts{
			Namespace: namespace,
			Name:      "get_latency_seconds",
			Help:      "Latency of GET operations",
			Buckets:   prometheus.DefBuckets,
		}),

		ReplicateLatency: promauto.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: namespace,
			Name:      "replicate_ack_latency_seconds",
			Help:      "Latency of replication acknowledgements per peer",
			Buckets:   prometheus.DefBuckets,
		}, []string{"peer"}),

		ReplicateAcks: promauto.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "replicate_acks_total",
			Help:      "Total replication acknowledgements",
		}, []string{"result"}),

		Errors: promauto.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "errors_total",
			Help:      "Total errors by type",
		}, []string{"type"}),

		WriteSuccessTotal: promauto.NewCounter(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "write_success_total",
			Help:      "Total successful write operations",
		}),

		WriteFailureTotal: promauto.NewCounter(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "write_failure_total",
			Help:      "Total failed write operations",
		}),

		ReadSuccessTotal: promauto.NewCounter(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "read_success_total",
			Help:      "Total successful read operations",
		}),

		ReadFailureTotal: promauto.NewCounter(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "read_failure_total",
			Help:      "Total failed read operations",
		}),

		CurrentR: promauto.NewGauge(prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "current_r",
			Help:      "Current read quorum size",
		}),

		CurrentW: promauto.NewGauge(prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "current_w",
			Help:      "Current write quorum size",
		}),

		HealthRTT: promauto.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "health_rtt_seconds",
			Help:      "Round trip time to peers",
		}, []string{"peer"}),

		RTTVariance: promauto.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "rtt_variance_ms2",
			Help:      "RTT variance per peer in milliseconds squared",
		}, []string{"peer"}),

		WriteOpsTotal: promauto.NewCounter(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "write_ops_total",
			Help:      "Total write operations",
		}),

		// adaptive quorum metrics
		CCSRaw: promauto.NewGauge(prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "ccs_raw",
			Help:      "Raw consistency confidence score",
		}),

		CCSSmoothed: promauto.NewGauge(prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "ccs_smoothed",
			Help:      "Smoothed consistency confidence score (10-sample moving average)",
		}),

		CCSComponentRTT: promauto.NewGauge(prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "ccs_component_rtt",
			Help:      "RTT health component of CCS",
		}),

		CCSComponentAvail: promauto.NewGauge(prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "ccs_component_avail",
			Help:      "Availability health component of CCS",
		}),

		CCSComponentVar: promauto.NewGauge(prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "ccs_component_var",
			Help:      "Variance health component of CCS",
		}),

		CCSComponentError: promauto.NewGauge(prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "ccs_component_error",
			Help:      "Error health component of CCS",
		}),

		QuorumAdjustments: promauto.NewCounter(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "quorum_adjustments_total",
			Help:      "Total number of quorum adjustments",
		}),

		QuorumAdjustmentReason: promauto.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "quorum_adjustment_reason_total",
			Help:      "Total number of quorum adjustments by reason",
		}, []string{"reason"}),

		HysteresisActive: promauto.NewGauge(prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "hysteresis_active",
			Help:      "Whether hysteresis lockout is currently active (1=active, 0=inactive)",
		}),

		// hlc and staleness metrics
		HLCDrift: promauto.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "hlc_drift_milliseconds",
			Help:      "Clock drift per peer in milliseconds",
		}, []string{"peer"}),

		StalenessViolations: promauto.NewCounter(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "staleness_violations_total",
			Help:      "Total staleness bound violations detected",
		}),

		StaleReadsRejected: promauto.NewCounter(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "stale_reads_rejected_total",
			Help:      "Total read operations rejected due to staleness",
		}),

		DataAge: promauto.NewHistogram(prometheus.HistogramOpts{
			Namespace: namespace,
			Name:      "data_age_seconds",
			Help:      "Distribution of data age on reads",
			Buckets:   []float64{0.1, 0.5, 1.0, 2.0, 3.0, 5.0, 10.0},
		}),

		// conflict and reconciliation metrics
		ConflictsDetected: promauto.NewCounter(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "conflicts_detected_total",
			Help:      "Total conflicts detected during reads or reconciliation",
		}),

		ConflictsResolved: promauto.NewCounter(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "conflicts_resolved_total",
			Help:      "Total conflicts resolved using LWW",
		}),

		ReconciliationRuns: promauto.NewCounter(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "reconciliation_runs_total",
			Help:      "Total reconciliation runs executed",
		}),

		ReconciliationKeys: promauto.NewHistogram(prometheus.HistogramOpts{
			Namespace: namespace,
			Name:      "reconciliation_keys",
			Help:      "Number of keys reconciled per run",
			Buckets:   prometheus.LinearBuckets(0, 10, 10),
		}),

		ReconciliationLatency: promauto.NewHistogram(prometheus.HistogramOpts{
			Namespace: namespace,
			Name:      "reconciliation_latency_seconds",
			Help:      "Duration of reconciliation operations",
			Buckets:   prometheus.DefBuckets,
		}),

		PartitionHealing: promauto.NewCounter(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "partition_healing_total",
			Help:      "Partition healing events detected (peer reconnections)",
		}),

		ReadRepair: promauto.NewCounter(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "read_repair_total",
			Help:      "Read repair operations performed",
		}),

		CCSComponentClock: promauto.NewGauge(prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "ccs_component_clock",
			Help:      "Clock health component of CCS",
		}),
	}

	return m
}

func (m *Metrics) RecordWriteSuccess() {
	m.WriteSuccessTotal.Inc()
	m.WriteOpsTotal.Inc()
}

func (m *Metrics) RecordWriteFailure() {
	m.WriteFailureTotal.Inc()
	m.WriteOpsTotal.Inc()
}

func (m *Metrics) RecordReadSuccess() {
	m.ReadSuccessTotal.Inc()
}

func (m *Metrics) RecordReadFailure() {
	m.ReadFailureTotal.Inc()
}
