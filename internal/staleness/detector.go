package staleness

import (
	"fmt"
	"time"

	"github.com/rachitkumar205/acp-kv/internal/hlc"
	"github.com/rachitkumar205/acp-kv/internal/metrics"
	"github.com/rachitkumar205/acp-kv/internal/storage"
)

// detector checks if data exceeds staleness bounds
type Detector struct {
	maxAge  time.Duration // maximum age before data is considered stale
	metrics *metrics.Metrics
}

// create new staleness detector
func NewDetector(maxAge time.Duration, m *metrics.Metrics) *Detector {
	return &Detector{
		maxAge:  maxAge,
		metrics: m,
	}
}

// check if value is stale
func (d *Detector) IsStale(timestamp hlc.HLC, now int64) bool {
	age := timestamp.Age(now)
	return age > d.maxAge
}

// calculate age of value
func (d *Detector) Age(timestamp hlc.HLC, now int64) time.Duration {
	return timestamp.Age(now)
}

// check staleness and return error if exceeded (strict mode)
func (d *Detector) CheckStrict(value storage.VersionedValue) error {
	now := time.Now().UnixNano()
	age := value.HLC.Age(now)

	if age > d.maxAge {
		d.metrics.StaleReadsRejected.Inc()
		d.metrics.StalenessViolations.Inc()

		return fmt.Errorf("staleness bound exceeded: data age %v > max %v",
			age, d.maxAge)
	}

	return nil
}

// check staleness for multiple values
func (d *Detector) CheckMultiple(values []storage.VersionedValue) ([]storage.VersionedValue, []storage.VersionedValue) {
	now := time.Now().UnixNano()
	fresh := []storage.VersionedValue{}
	stale := []storage.VersionedValue{}

	for _, v := range values {
		if d.IsStale(v.HLC, now) {
			stale = append(stale, v)
		} else {
			fresh = append(fresh, v)
		}
	}

	return fresh, stale
}
