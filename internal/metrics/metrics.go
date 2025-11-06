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
	HealthRTT *prometheus.GaugeVec
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
	}

	return m
}

func (m *Metrics) RecordWriteSuccess() {
	m.WriteSuccessTotal.Inc()
}

func (m *Metrics) RecordWriteFailure() {
	m.WriteFailureTotal.Inc()
}

func (m *Metrics) RecordReadSuccess() {
	m.ReadSuccessTotal.Inc()
}

func (m *Metrics) RecordReadFailure() {
	m.ReadFailureTotal.Inc()
}
