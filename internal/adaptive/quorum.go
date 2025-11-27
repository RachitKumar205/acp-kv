package adaptive

import (
	"fmt"
	"sync"
	"time"

	"github.com/rachitkumar205/acp-kv/internal/config"
	"github.com/rachitkumar205/acp-kv/internal/metrics"
	"go.uber.org/zap"
)

// QuorumProvider defines interface for accessing quorum parameters
type QuorumProvider interface {
	GetR() int
	GetW() int
	GetN() int
}

// AdaptiveQuorum manages dynamic quorum parameters with thread-safe access
type AdaptiveQuorum struct {
	mu sync.RWMutex

	// current quorum values
	currentR int
	currentW int
	n        int

	// bounds for adjustments
	minR int
	maxR int
	minW int
	maxW int

	// hysteresis state
	lastAdjustTime  time.Time
	lockoutDuration time.Duration

	// dependencies
	logger  *zap.Logger
	metrics *metrics.Metrics
}

// NewAdaptiveQuorum creates a new adaptive quorum manager
func NewAdaptiveQuorum(
	initialR, initialW, n int,
	minR, maxR, minW, maxW int,
	logger *zap.Logger,
	m *metrics.Metrics,
) *AdaptiveQuorum {
	return &AdaptiveQuorum{
		currentR:        initialR,
		currentW:        initialW,
		n:               n,
		minR:            minR,
		maxR:            maxR,
		minW:            minW,
		maxW:            maxW,
		lockoutDuration: 5 * time.Second,
		logger:          logger,
		metrics:         m,
	}
}

// GetR returns current read quorum size (thread-safe)
func (aq *AdaptiveQuorum) GetR() int {
	aq.mu.RLock()
	defer aq.mu.RUnlock()
	return aq.currentR
}

// GetW returns current write quorum size (thread-safe)
func (aq *AdaptiveQuorum) GetW() int {
	aq.mu.RLock()
	defer aq.mu.RUnlock()
	return aq.currentW
}

// GetN returns cluster size
func (aq *AdaptiveQuorum) GetN() int {
	return aq.n
}

// SetQuorum atomically updates quorum parameters with validation
func (aq *AdaptiveQuorum) SetQuorum(newR, newW int, reason string) error {
	aq.mu.Lock()
	defer aq.mu.Unlock()

	// check hysteresis lockout
	if time.Since(aq.lastAdjustTime) < aq.lockoutDuration {
		return fmt.Errorf("adjustment rejected: in hysteresis lockout period")
	}

	// validate quorum intersection (r + w > n)
	if newR+newW <= aq.n {
		return fmt.Errorf("quorum intersection violated: r=%d + w=%d <= n=%d", newR, newW, aq.n)
	}

	// validate bounds
	if newR < aq.minR || newR > aq.maxR {
		return fmt.Errorf("r=%d outside bounds [%d, %d]", newR, aq.minR, aq.maxR)
	}
	if newW < aq.minW || newW > aq.maxW {
		return fmt.Errorf("w=%d outside bounds [%d, %d]", newW, aq.minW, aq.maxW)
	}

	// store old values for logging
	oldR := aq.currentR
	oldW := aq.currentW

	// apply changes
	aq.currentR = newR
	aq.currentW = newW
	aq.lastAdjustTime = time.Now()

	// update prometheus metrics
	aq.metrics.CurrentR.Set(float64(newR))
	aq.metrics.CurrentW.Set(float64(newW))

	// log adjustment
	aq.logger.Info("quorum adjusted",
		zap.Int("old_r", oldR),
		zap.Int("new_r", newR),
		zap.Int("old_w", oldW),
		zap.Int("new_w", newW),
		zap.String("reason", reason))

	return nil
}

// IsInLockout returns true if currently in hysteresis lockout period
func (aq *AdaptiveQuorum) IsInLockout() bool {
	aq.mu.RLock()
	defer aq.mu.RUnlock()
	return time.Since(aq.lastAdjustTime) < aq.lockoutDuration
}

// Validate checks if given r and w satisfy quorum requirements
func (aq *AdaptiveQuorum) Validate(r, w int) error {
	if r+w <= aq.n {
		return fmt.Errorf("quorum intersection violated: r=%d + w=%d <= n=%d", r, w, aq.n)
	}
	if r < aq.minR || r > aq.maxR {
		return fmt.Errorf("r=%d outside bounds [%d, %d]", r, aq.minR, aq.maxR)
	}
	if w < aq.minW || w > aq.maxW {
		return fmt.Errorf("w=%d outside bounds [%d, %d]", w, aq.minW, aq.maxW)
	}
	return nil
}

// implement quorumprovider interface for config (static mode)
var _ QuorumProvider = (*config.Config)(nil)
