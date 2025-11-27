package hlc

import (
	"testing"
	"time"
)

func TestClock_Now(t *testing.T) {
	clock := NewClock("node1", 500*time.Millisecond)

	// generate first timestamp
	ts1 := clock.Now()
	if ts1.IsZero() {
		t.Fatal("expected non-zero timestamp")
	}
	if ts1.NodeID != "node1" {
		t.Errorf("expected node1, got %s", ts1.NodeID)
	}

	// generate second timestamp immediately
	ts2 := clock.Now()
	if !ts2.HappensAfter(ts1) {
		t.Error("expected ts2 after ts1 (monotonicity)")
	}

	// third timestamp should also be after
	ts3 := clock.Now()
	if !ts3.HappensAfter(ts2) {
		t.Error("expected ts3 after ts2")
	}
}

func TestClock_Monotonicity(t *testing.T) {
	clock := NewClock("node1", 500*time.Millisecond)

	// generate many timestamps rapidly
	var prev HLC
	for i := 0; i < 1000; i++ {
		ts := clock.Now()
		if i > 0 && !ts.HappensAfter(prev) {
			t.Fatalf("monotonicity violated at iteration %d: %v not after %v", i, ts, prev)
		}
		prev = ts
	}
}

func TestClock_Update(t *testing.T) {
	clock1 := NewClock("node1", 500*time.Millisecond)
	clock2 := NewClock("node2", 500*time.Millisecond)

	// node1 generates timestamp
	ts1 := clock1.Now()

	// node2 receives ts1 and updates
	err := clock2.Update(ts1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// node2 generates new timestamp
	ts2 := clock2.Now()

	// ts2 should happen after ts1
	if !ts2.HappensAfter(ts1) {
		t.Errorf("expected ts2 after ts1: ts1=%v, ts2=%v", ts1, ts2)
	}
}

func TestClock_UpdateWithDrift(t *testing.T) {
	clock := NewClock("node1", 100*time.Millisecond)

	// create remote timestamp far in future
	future := HLC{
		Physical: time.Now().Add(1 * time.Second).UnixNano(),
		Logical:  0,
		NodeID:   "node2",
	}

	// update should fail due to excessive drift
	err := clock.Update(future)
	if err == nil {
		t.Error("expected error for excessive clock drift")
	}
}

func TestHLC_HappensBefore(t *testing.T) {
	tests := []struct {
		name     string
		h1       HLC
		h2       HLC
		expected bool
	}{
		{
			name:     "earlier physical time",
			h1:       HLC{Physical: 100, Logical: 0, NodeID: "n1"},
			h2:       HLC{Physical: 200, Logical: 0, NodeID: "n2"},
			expected: true,
		},
		{
			name:     "same physical, lower logical",
			h1:       HLC{Physical: 100, Logical: 5, NodeID: "n1"},
			h2:       HLC{Physical: 100, Logical: 10, NodeID: "n2"},
			expected: true,
		},
		{
			name:     "later physical time",
			h1:       HLC{Physical: 200, Logical: 0, NodeID: "n1"},
			h2:       HLC{Physical: 100, Logical: 0, NodeID: "n2"},
			expected: false,
		},
		{
			name:     "same physical, higher logical",
			h1:       HLC{Physical: 100, Logical: 10, NodeID: "n1"},
			h2:       HLC{Physical: 100, Logical: 5, NodeID: "n2"},
			expected: false,
		},
		{
			name:     "equal timestamps",
			h1:       HLC{Physical: 100, Logical: 5, NodeID: "n1"},
			h2:       HLC{Physical: 100, Logical: 5, NodeID: "n2"},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.h1.HappensBefore(tt.h2)
			if result != tt.expected {
				t.Errorf("expected %v, got %v for %v < %v", tt.expected, result, tt.h1, tt.h2)
			}
		})
	}
}

func TestHLC_IsConcurrentWith(t *testing.T) {
	// concurrent events have no causal relationship
	h1 := HLC{Physical: 100, Logical: 5, NodeID: "n1"}
	h2 := HLC{Physical: 100, Logical: 5, NodeID: "n2"}

	if !h1.IsConcurrentWith(h2) {
		t.Error("expected concurrent timestamps")
	}

	// events with happens-before relationship are not concurrent
	h3 := HLC{Physical: 100, Logical: 6, NodeID: "n3"}
	if h1.IsConcurrentWith(h3) {
		t.Error("expected non-concurrent (h3 after h1)")
	}
}

func TestHLC_Compare(t *testing.T) {
	h1 := HLC{Physical: 100, Logical: 5, NodeID: "n1"}
	h2 := HLC{Physical: 200, Logical: 3, NodeID: "n2"}
	h3 := HLC{Physical: 100, Logical: 5, NodeID: "n3"}

	// h1 < h2
	if h1.Compare(h2) != -1 {
		t.Error("expected h1 < h2")
	}

	// h2 > h1
	if h2.Compare(h1) != 1 {
		t.Error("expected h2 > h1")
	}

	// h1 == h3 (concurrent, same physical/logical)
	if h1.Compare(h3) != 0 {
		t.Error("expected h1 concurrent with h3")
	}
}

func TestHLC_Age(t *testing.T) {
	now := time.Now().UnixNano()
	past := now - int64(5*time.Second)

	h := HLC{Physical: past, Logical: 0, NodeID: "n1"}
	age := h.Age(now)

	if age < 4*time.Second || age > 6*time.Second {
		t.Errorf("expected age ~5s, got %v", age)
	}

	// future timestamps have zero age
	future := now + int64(5*time.Second)
	hFuture := HLC{Physical: future, Logical: 0, NodeID: "n1"}
	futureAge := hFuture.Age(now)
	if futureAge != 0 {
		t.Errorf("expected zero age for future timestamp, got %v", futureAge)
	}
}

func TestHLC_Equal(t *testing.T) {
	h1 := HLC{Physical: 100, Logical: 5, NodeID: "n1"}
	h2 := HLC{Physical: 100, Logical: 5, NodeID: "n2"}
	h3 := HLC{Physical: 100, Logical: 6, NodeID: "n1"}

	if !h1.Equal(h2) {
		t.Error("expected h1 equal h2")
	}

	if h1.Equal(h3) {
		t.Error("expected h1 not equal h3")
	}
}

func TestClock_LogicalIncrement(t *testing.T) {
	clock := NewClock("node1", 500*time.Millisecond)

	// generate many timestamps rapidly in tight loop
	// at least some should have same physical time and increment logical
	var prevPhysical int64
	var prevLogical int64
	logicalIncremented := false

	for i := 0; i < 100; i++ {
		ts := clock.Now()
		if ts.Physical == prevPhysical && ts.Logical > prevLogical {
			logicalIncremented = true
			break
		}
		prevPhysical = ts.Physical
		prevLogical = ts.Logical
	}

	if !logicalIncremented {
		t.Error("expected logical counter to increment for at least one timestamp with same physical time")
	}
}

func TestClock_CausalityPreservation(t *testing.T) {
	// simulate three nodes exchanging messages
	node1 := NewClock("node1", 500*time.Millisecond)
	node2 := NewClock("node2", 500*time.Millisecond)
	node3 := NewClock("node3", 500*time.Millisecond)

	// node1: event A
	eventA := node1.Now()

	// node2 receives message with eventA
	node2.Update(eventA)

	// node2: event B (happens after A)
	eventB := node2.Now()
	if !eventB.HappensAfter(eventA) {
		t.Error("causality violated: B should happen after A")
	}

	// node3 receives message with eventB
	node3.Update(eventB)

	// node3: event C (happens after B, transitively after A)
	eventC := node3.Now()
	if !eventC.HappensAfter(eventB) {
		t.Error("causality violated: C should happen after B")
	}
	if !eventC.HappensAfter(eventA) {
		t.Error("transitivity violated: C should happen after A")
	}
}

func TestHLC_IsZero(t *testing.T) {
	zero := HLC{}
	if !zero.IsZero() {
		t.Error("expected zero HLC")
	}

	nonZero := HLC{Physical: 1, Logical: 0, NodeID: "n1"}
	if nonZero.IsZero() {
		t.Error("expected non-zero HLC")
	}
}

func TestClock_ConcurrentEvents(t *testing.T) {
	// two nodes generate events independently
	node1 := NewClock("node1", 500*time.Millisecond)
	node2 := NewClock("node2", 500*time.Millisecond)

	// both generate events at "same time" (no message exchange)
	event1 := node1.Now()
	event2 := node2.Now()

	// events should be concurrent if physical times are close
	// (may not be exactly concurrent due to test execution timing)
	if event1.Physical == event2.Physical && event1.Logical == event2.Logical {
		if !event1.IsConcurrentWith(event2) {
			t.Error("expected concurrent events")
		}
	}
}
