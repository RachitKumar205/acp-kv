package metrics

import (
	"fmt"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/client_golang/prometheus"
)

// metricsreader provides real-time access to prometheus metric values
// by reading directly from the registry without network calls
type MetricsReader struct {
	metrics *Metrics
}

// histogramstats contains extracted statistics from a histogram
type HistogramStats struct {
	Count uint64  // total number of observations
	Sum   float64 // sum of all observations
	Avg   float64 // average value
	P95   float64 // estimated 95th percentile
}

// newmetricsreader creates a new metrics reader
func NewMetricsReader(m *Metrics) *MetricsReader {
	return &MetricsReader{metrics: m}
}

// getcountervalue reads the current value of a counter
func (r *MetricsReader) GetCounterValue(counter prometheus.Counter) (float64, error) {
	var metricDto dto.Metric
	if err := counter.(prometheus.Metric).Write(&metricDto); err != nil {
		return 0, err
	}
	return metricDto.GetCounter().GetValue(), nil
}

// getgaugevalue reads the current value of a gauge
func (r *MetricsReader) GetGaugeValue(gauge prometheus.Gauge) (float64, error) {
	var metricDto dto.Metric
	if err := gauge.(prometheus.Metric).Write(&metricDto); err != nil {
		return 0, err
	}
	return metricDto.GetGauge().GetValue(), nil
}

// getwritesuccessrate calculates the write success rate from counters
func (r *MetricsReader) GetWriteSuccessRate() float64 {
	success, err := r.GetCounterValue(r.metrics.WriteSuccessTotal)
	if err != nil {
		return 1.0 // assume healthy if no data
	}

	failure, err := r.GetCounterValue(r.metrics.WriteFailureTotal)
	if err != nil {
		return 1.0
	}

	total := success + failure
	if total == 0 {
		return 1.0 // no operations yet, assume healthy
	}

	return success / total
}

// gethistogramstats extracts statistics from a histogram observer
func (r *MetricsReader) GetHistogramStats(hist prometheus.Observer) (*HistogramStats, error) {
	var metricDto dto.Metric
	if err := hist.(prometheus.Metric).Write(&metricDto); err != nil {
		return nil, err
	}

	h := metricDto.GetHistogram()
	stats := &HistogramStats{
		Count: h.GetSampleCount(),
		Sum:   h.GetSampleSum(),
	}

	// calculate average
	if stats.Count > 0 {
		stats.Avg = stats.Sum / float64(stats.Count)
	}

	// estimate p95 from histogram buckets
	stats.P95 = r.estimatePercentile(h, 0.95)

	return stats, nil
}

// estimatepercentile estimates a percentile from histogram buckets
func (r *MetricsReader) estimatePercentile(hist *dto.Histogram, percentile float64) float64 {
	totalCount := hist.GetSampleCount()
	if totalCount == 0 {
		return 0
	}

	target := float64(totalCount) * percentile
	cumulativeCount := uint64(0)

	for _, bucket := range hist.GetBucket() {
		cumulativeCount = bucket.GetCumulativeCount()
		if float64(cumulativeCount) >= target {
			return bucket.GetUpperBound()
		}
	}

	return 0
}

// getpeerlatencystats gets latency statistics for a specific peer
func (r *MetricsReader) GetPeerLatencyStats(peer string) (*HistogramStats, error) {
	observer, err := r.metrics.ReplicateLatency.GetMetricWithLabelValues(peer)
	if err != nil {
		return nil, fmt.Errorf("failed to get metric for peer %s: %w", peer, err)
	}
	return r.GetHistogramStats(observer)
}

// getallpeerslatencystats aggregates latency statistics across all known peers
func (r *MetricsReader) GetAllPeersLatencyStats(peers []string) (*HistogramStats, error) {
	if len(peers) == 0 {
		return &HistogramStats{}, nil
	}

	totalCount := uint64(0)
	totalSum := 0.0
	maxP95 := 0.0
	successfulPeers := 0 // NEW: track how many peers responded

	for _, peer := range peers {
		stats, err := r.GetPeerLatencyStats(peer)
		if err != nil {
			// skip peers with no data yet (peer might be down)
			continue
		}

		totalCount += stats.Count
		totalSum += stats.Sum
		if stats.P95 > maxP95 {
			maxP95 = stats.P95
		}
		successfulPeers++ // NEW: increment on success
	}

	result := &HistogramStats{
		Count: uint64(successfulPeers), // CHANGED: use peer count, not sample count
		Sum:   totalSum,
		P95:   maxP95, // use max p95 across peers (worst case)
	}

	if totalCount > 0 {
		result.Avg = totalSum / float64(totalCount)
	}

	return result, nil
}

// gethealthrtt gets the health probe rtt for a specific peer
func (r *MetricsReader) GetHealthRTT(peer string) (float64, error) {
	gauge, err := r.metrics.HealthRTT.GetMetricWithLabelValues(peer)
	if err != nil {
		return 0, fmt.Errorf("failed to get health rtt for peer %s: %w", peer, err)
	}
	return r.GetGaugeValue(gauge)
}

// getaveragehealthrtt calculates average health rtt across all peers
func (r *MetricsReader) GetAverageHealthRTT(peers []string) float64 {
	if len(peers) == 0 {
		return 0
	}

	totalRTT := 0.0
	validCount := 0

	for _, peer := range peers {
		rtt, err := r.GetHealthRTT(peer)
		if err != nil {
			continue
		}
		if rtt > 0 { // only count valid rtts
			totalRTT += rtt
			validCount++
		}
	}

	if validCount == 0 {
		return 0
	}

	return totalRTT / float64(validCount)
}

// getclockdrift gets the hlc clock drift for a specific peer
func (r *MetricsReader) GetClockDrift(peer string) (float64, error) {
	gauge, err := r.metrics.HLCDrift.GetMetricWithLabelValues(peer)
	if err != nil {
		return 0, fmt.Errorf("failed to get clock drift for peer %s: %w", peer, err)
	}
	// convert milliseconds to seconds
	driftMS, err := r.GetGaugeValue(gauge)
	return driftMS / 1000.0, err
}

// getclockdriftstats calculates average clock drift across all peers
func (r *MetricsReader) GetClockDriftStats(peers []string) float64 {
	if len(peers) == 0 {
		return 0
	}

	totalDrift := 0.0
	validCount := 0

	for _, peer := range peers {
		drift, err := r.GetClockDrift(peer)
		if err != nil {
			continue
		}
		if drift >= 0 { // count all non-negative drifts
			totalDrift += drift
			validCount++
		}
	}

	if validCount == 0 {
		return 0 // no clock drift data, assume perfect sync
	}

	return totalDrift / float64(validCount)
}
