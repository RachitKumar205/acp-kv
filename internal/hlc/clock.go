package hlc

import (
	"fmt"
	"sync"
	"time"
)

// hybrid logical clock timestamp
type HLC struct {
	Physical int64  // physical timestamp in nanoseconds
	Logical  int64  // logical counter for concurrent events
	NodeID   string // node that generated this timestamp
}

// thread-safe hybrid logical clock
type Clock struct {
	mu       sync.Mutex
	physical int64         // last physical time observed
	logical  int64         // current logical counter
	nodeID   string        // this node's identifier
	maxDrift time.Duration // maximum allowed clock drift
}

// create new hlc clock
func NewClock(nodeID string, maxDrift time.Duration) *Clock {
	return &Clock{
		physical: time.Now().UnixNano(),
		logical:  0,
		nodeID:   nodeID,
		maxDrift: maxDrift,
	}
}

// generate new hlc timestamp (monotonic)
func (c *Clock) Now() HLC {
	c.mu.Lock()
	defer c.mu.Unlock()

	physicalNow := time.Now().UnixNano()

	if physicalNow > c.physical {
		// physical time advanced
		c.physical = physicalNow
		c.logical = 0
	} else {
		// physical time same or went backward
		// increment logical counter for monotonicity
		c.logical++
	}

	return HLC{
		Physical: c.physical,
		Logical:  c.logical,
		NodeID:   c.nodeID,
	}
}

// update local clock with remote timestamp
func (c *Clock) Update(remote HLC) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	physicalNow := time.Now().UnixNano()

	// check for excessive clock drift
	drift := remote.Physical - physicalNow
	if drift > c.maxDrift.Nanoseconds() {
		return fmt.Errorf("clock drift too large: remote %d ahead of local %d (max: %v)",
			remote.Physical, physicalNow, c.maxDrift)
	}

	if remote.Physical > c.physical {
		// remote physical ahead
		c.physical = remote.Physical
		c.logical = remote.Logical + 1
	} else if remote.Physical == c.physical {
		// equal physical, use max logical
		if remote.Logical > c.logical {
			c.logical = remote.Logical + 1
		} else {
			c.logical++
		}
	} else {
		// local physical ahead
		c.logical++
	}

	// advance if current time ahead of both
	if physicalNow > c.physical {
		c.physical = physicalNow
		c.logical = 0
	}

	return nil
}

// check if h happened before other
func (h HLC) HappensBefore(other HLC) bool {
	if h.Physical < other.Physical {
		return true
	}
	if h.Physical == other.Physical && h.Logical < other.Logical {
		return true
	}
	return false
}

// check if h happened after other
func (h HLC) HappensAfter(other HLC) bool {
	return other.HappensBefore(h)
}

// check if h and other are concurrent
func (h HLC) IsConcurrentWith(other HLC) bool {
	return !h.HappensBefore(other) && !h.HappensAfter(other)
}

// check if timestamps are identical
func (h HLC) Equal(other HLC) bool {
	return h.Physical == other.Physical && h.Logical == other.Logical
}

// compare timestamps (-1: before, 0: concurrent/equal, +1: after)
func (h HLC) Compare(other HLC) int {
	if h.HappensBefore(other) {
		return -1
	}
	if h.HappensAfter(other) {
		return 1
	}
	return 0
}

// calculate age of timestamp
func (h HLC) Age(now int64) time.Duration {
	if now > h.Physical {
		return time.Duration(now - h.Physical)
	}
	return 0
}

// human-readable timestamp representation
func (h HLC) String() string {
	physicalTime := time.Unix(0, h.Physical)
	return fmt.Sprintf("HLC{physical=%s, logical=%d, node=%s}",
		physicalTime.Format(time.RFC3339Nano), h.Logical, h.NodeID)
}

// check if zero-value hlc
func (h HLC) IsZero() bool {
	return h.Physical == 0 && h.Logical == 0
}

// convert to single int64 (loses logical component)
func (h HLC) ToNanos() int64 {
	return h.Physical
}
