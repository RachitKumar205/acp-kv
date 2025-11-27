package adaptive

import (
	"math"
	"sync"

	"github.com/rachitkumar205/acp-kv/internal/metrics"
	"go.uber.org/zap"
)

// metricswindow implements a circular buffer for sliding window metrics
type MetricsWindow struct {
	samples []float64
	size    int
	index   int
	count   int
	mu      sync.RWMutex
}

// newmetricswindow creates a new metrics window with given capacity
func NewMetricsWindow(size int) *MetricsWindow {
	return &MetricsWindow{
		samples: make([]float64, size),
		size:    size,
	}
}

// add inserts a new sample into the window
func (mw *MetricsWindow) Add(value float64) {
	mw.mu.Lock()
	defer mw.mu.Unlock()

	mw.samples[mw.index] = value
	mw.index = (mw.index + 1) % mw.size
	if mw.count < mw.size {
		mw.count++
	}
}

// getaverage calculates the average of all samples in the window
func (mw *MetricsWindow) GetAverage() float64 {
	mw.mu.RLock()
	defer mw.mu.RUnlock()

	if mw.count == 0 {
		return 0
	}

	sum := 0.0
	for i := 0; i < mw.count; i++ {
		sum += mw.samples[i]
	}
	return sum / float64(mw.count)
}

// getvariance calculates variance of samples in the window
func (mw *MetricsWindow) GetVariance() float64 {
	mw.mu.RLock()
	defer mw.mu.RUnlock()

	if mw.count == 0 {
		return 0
	}

	// calculate mean
	mean := 0.0
	for i := 0; i < mw.count; i++ {
		mean += mw.samples[i]
	}
	mean /= float64(mw.count)

	// calculate variance
	variance := 0.0
	for i := 0; i < mw.count; i++ {
		diff := mw.samples[i] - mean
		variance += diff * diff
	}
	return variance / float64(mw.count)
}

// ccscomponents holds the breakdown of ccs calculation
type CCSComponents struct {
	RTTHealth   float64
	AvailHealth float64
	VarHealth   float64
	ErrorHealth float64
	ClockHealth float64 // hlc clock drift health
}

// ccscomputer computes consistency confidence score from metrics
type CCSComputer struct {
	mu sync.RWMutex

	// sliding windows for metrics
	rttWindow      *MetricsWindow
	successWindow  *MetricsWindow
	varianceWindow *MetricsWindow
	errorWindow    *MetricsWindow
	clockWindow    *MetricsWindow

	// ccs history for smoothing
	ccsHistory *MetricsWindow

	// weights for ccs components (must sum to 1.0)
	alphaRTT     float64 // weight for rtt health
	betaAvail    float64 // weight for availability health
	gammaVar     float64 // weight for variance health
	deltaError   float64 // weight for error health
	epsilonClock float64 // weight for clock health

	// thresholds
	rttBadThreshold   float64 // 200ms - rtt considered bad
	varBadThreshold   float64 // 50ms² - variance considered high
	clockBadThreshold float64 // 100ms - clock drift considered bad

	logger  *zap.Logger
	metrics *metrics.Metrics
}

// newccscomputer creates a new ccs computation engine
func NewCCSComputer(logger *zap.Logger, m *metrics.Metrics) *CCSComputer {
	return &CCSComputer{
		rttWindow:      NewMetricsWindow(10),
		successWindow:  NewMetricsWindow(10),
		varianceWindow: NewMetricsWindow(10),
		errorWindow:    NewMetricsWindow(10),
		clockWindow:    NewMetricsWindow(10),
		ccsHistory:     NewMetricsWindow(10),
		alphaRTT:        0.20, // rtt health
		betaAvail:       0.40, // INCREASED - availability is critical
		gammaVar:        0.15, // variance health
		deltaError:      0.15, // error health
		epsilonClock:    0.10, // clock health
		rttBadThreshold:   0.2,    // 200ms
		varBadThreshold:   0.0025, // 50ms² = 0.05²
		clockBadThreshold: 0.1,    // 100ms
		logger:            logger,
		metrics:           m,
	}
}

// recordmetrics records a new set of metrics for ccs computation
func (cc *CCSComputer) RecordMetrics(avgRTT, successRate, variance, errorRate, clockDrift float64) {
	cc.mu.Lock()
	defer cc.mu.Unlock()

	cc.rttWindow.Add(avgRTT)
	cc.successWindow.Add(successRate)
	cc.varianceWindow.Add(variance)
	cc.errorWindow.Add(errorRate)
	cc.clockWindow.Add(clockDrift)
}

// computeccs calculates the raw ccs from current metrics
func (cc *CCSComputer) ComputeCCS() (float64, CCSComponents) {
	cc.mu.RLock()
	defer cc.mu.RUnlock()

	// get latest values from windows
	avgRTT := cc.rttWindow.GetAverage()
	successRate := cc.successWindow.GetAverage()
	variance := cc.varianceWindow.GetAverage()
	errorRate := cc.errorWindow.GetAverage()
	clockDrift := cc.clockWindow.GetAverage()

	// calculate health components
	// rttHealth: 1 - min(avgRTT / 200ms, 1)
	rttHealth := 1.0 - math.Min(avgRTT/cc.rttBadThreshold, 1.0)

	// availHealth: directly use success rate
	availHealth := successRate

	// varHealth: 1 - min(variance / 50ms², 1)
	varHealth := 1.0 - math.Min(variance/cc.varBadThreshold, 1.0)

	// errorHealth: 1 - errorRate
	errorHealth := 1.0 - errorRate

	// clockHealth: 1 - min(clockDrift / 100ms, 1)
	clockHealth := 1.0 - math.Min(clockDrift/cc.clockBadThreshold, 1.0)

	// weighted sum (weights sum to 1.0)
	ccs := cc.alphaRTT*rttHealth +
		cc.betaAvail*availHealth +
		cc.gammaVar*varHealth +
		cc.deltaError*errorHealth +
		cc.epsilonClock*clockHealth

	components := CCSComponents{
		RTTHealth:   rttHealth,
		AvailHealth: availHealth,
		VarHealth:   varHealth,
		ErrorHealth: errorHealth,
		ClockHealth: clockHealth,
	}

	return ccs, components
}

// addtoccshistory adds a ccs value to the history for smoothing
func (cc *CCSComputer) AddToCCSHistory(ccs float64) {
	cc.ccsHistory.Add(ccs)
}

// getsmoothedccs returns the 10-sample moving average of ccs
func (cc *CCSComputer) GetSmoothedCCS() float64 {
	return cc.ccsHistory.GetAverage()
}

// updatemetricsguages updates prometheus gauges for ccs components
func (cc *CCSComputer) UpdateMetricsGauges(rawCCS, smoothedCCS float64, components CCSComponents) {
	cc.metrics.CCSRaw.Set(rawCCS)
	cc.metrics.CCSSmoothed.Set(smoothedCCS)
	cc.metrics.CCSComponentRTT.Set(components.RTTHealth)
	cc.metrics.CCSComponentAvail.Set(components.AvailHealth)
	cc.metrics.CCSComponentVar.Set(components.VarHealth)
	cc.metrics.CCSComponentError.Set(components.ErrorHealth)
	cc.metrics.CCSComponentClock.Set(components.ClockHealth)
}
