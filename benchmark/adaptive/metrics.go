package adaptive

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"
)

// MetricsCollector collects ACP metrics from Prometheus
type MetricsCollector struct {
	prometheusURL string
	mu            sync.RWMutex
	snapshot      MetricsSnapshot
	client        *http.Client
}

// MetricsSnapshot represents a point-in-time snapshot of ACP metrics
type MetricsSnapshot struct {
	Timestamp            time.Time
	CCSRaw               float64
	CCSSmoothed          float64
	CurrentR             int
	CurrentW             int
	QuorumAdjustments    int64
	StalenessViolations  int64
	ConflictsDetected    int64
	ConflictsResolved    int64
	ReadLatencyP95       float64
	WriteLatencyP95      float64
	Throughput           float64
	SuccessRate          float64
}

// NewMetricsCollector creates a new metrics collector
func NewMetricsCollector(prometheusURL string) *MetricsCollector {
	return &MetricsCollector{
		prometheusURL: prometheusURL,
		client: &http.Client{
			Timeout: 5 * time.Second,
		},
	}
}

// Update fetches latest metrics from Prometheus
func (m *MetricsCollector) Update(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	snapshot := MetricsSnapshot{
		Timestamp: time.Now(),
	}

	// query all metrics
	metrics := map[string]*float64{
		"acp_ccs_raw":                      &snapshot.CCSRaw,
		"acp_ccs_smoothed":                 &snapshot.CCSSmoothed,
		"acp_current_r":                    nil, // handle separately (int)
		"acp_current_w":                    nil, // handle separately (int)
		"acp_quorum_adjustments_total":     nil, // handle separately (int64)
		"acp_staleness_violations_total":   nil, // handle separately (int64)
		"acp_conflicts_detected_total":     nil, // handle separately (int64)
		"acp_conflicts_resolved_total":     nil, // handle separately (int64)
	}

	// fetch float metrics
	for metricName, target := range metrics {
		if target == nil {
			continue
		}
		value, err := m.queryMetric(ctx, metricName)
		if err != nil {
			// non-critical metrics, just log and continue
			continue
		}
		*target = value
	}

	// fetch int metrics
	if val, err := m.queryMetric(ctx, "acp_current_r"); err == nil {
		snapshot.CurrentR = int(val)
	}
	if val, err := m.queryMetric(ctx, "acp_current_w"); err == nil {
		snapshot.CurrentW = int(val)
	}
	if val, err := m.queryMetric(ctx, "acp_quorum_adjustments_total"); err == nil {
		snapshot.QuorumAdjustments = int64(val)
	}
	if val, err := m.queryMetric(ctx, "acp_staleness_violations_total"); err == nil {
		snapshot.StalenessViolations = int64(val)
	}
	if val, err := m.queryMetric(ctx, "acp_conflicts_detected_total"); err == nil {
		snapshot.ConflictsDetected = int64(val)
	}
	if val, err := m.queryMetric(ctx, "acp_conflicts_resolved_total"); err == nil {
		snapshot.ConflictsResolved = int64(val)
	}

	// fetch percentile metrics
	if val, err := m.queryMetric(ctx, `histogram_quantile(0.95, rate(acp_get_latency_seconds_bucket[1m]))`); err == nil {
		snapshot.ReadLatencyP95 = val * 1000 // convert to ms
	}
	if val, err := m.queryMetric(ctx, `histogram_quantile(0.95, rate(acp_put_latency_seconds_bucket[1m]))`); err == nil {
		snapshot.WriteLatencyP95 = val * 1000 // convert to ms
	}

	// calculate throughput (ops/sec)
	if val, err := m.queryMetric(ctx, `rate(acp_gets_total[1m]) + rate(acp_puts_total[1m])`); err == nil {
		snapshot.Throughput = val
	}

	// calculate success rate
	successReads, err1 := m.queryMetric(ctx, `rate(acp_reads_success_total[1m])`)
	successWrites, err2 := m.queryMetric(ctx, `rate(acp_writes_success_total[1m])`)
	failedReads, err3 := m.queryMetric(ctx, `rate(acp_reads_failure_total[1m])`)
	failedWrites, err4 := m.queryMetric(ctx, `rate(acp_writes_failure_total[1m])`)

	if err1 == nil && err2 == nil && err3 == nil && err4 == nil {
		total := successReads + successWrites + failedReads + failedWrites
		if total > 0 {
			snapshot.SuccessRate = (successReads + successWrites) / total
		}
	}

	m.snapshot = snapshot
	return nil
}

// queryMetric executes a PromQL query and returns the result
func (m *MetricsCollector) queryMetric(ctx context.Context, query string) (float64, error) {
	queryURL := fmt.Sprintf("%s/api/v1/query", m.prometheusURL)

	params := url.Values{}
	params.Add("query", query)

	req, err := http.NewRequestWithContext(ctx, "GET", queryURL+"?"+params.Encode(), nil)
	if err != nil {
		return 0, err
	}

	resp, err := m.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return 0, fmt.Errorf("prometheus query failed: %s - %s", resp.Status, string(body))
	}

	var result struct {
		Status string `json:"status"`
		Data   struct {
			ResultType string `json:"resultType"`
			Result     []struct {
				Metric map[string]string `json:"metric"`
				Value  []interface{}     `json:"value"`
			} `json:"result"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, err
	}

	if result.Status != "success" {
		return 0, fmt.Errorf("prometheus query status: %s", result.Status)
	}

	if len(result.Data.Result) == 0 {
		return 0, fmt.Errorf("no data returned")
	}

	// extract value
	if len(result.Data.Result[0].Value) < 2 {
		return 0, fmt.Errorf("invalid result format")
	}

	valueStr, ok := result.Data.Result[0].Value[1].(string)
	if !ok {
		return 0, fmt.Errorf("value is not a string")
	}

	value, err := strconv.ParseFloat(valueStr, 64)
	if err != nil {
		return 0, fmt.Errorf("failed to parse value: %w", err)
	}

	return value, nil
}

// Snapshot returns a copy of the current metrics snapshot
func (m *MetricsCollector) Snapshot() MetricsSnapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.snapshot
}

// GetCCSRaw returns the raw CCS value
func (m *MetricsCollector) GetCCSRaw() float64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.snapshot.CCSRaw
}

// GetCCSSmoothed returns the smoothed CCS value
func (m *MetricsCollector) GetCCSSmoothed() float64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.snapshot.CCSSmoothed
}

// GetCurrentQuorum returns the current R and W values
func (m *MetricsCollector) GetCurrentQuorum() (int, int) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.snapshot.CurrentR, m.snapshot.CurrentW
}
