package staleness

import (
	"testing"
	"time"

	"github.com/rachitkumar205/acp-kv/internal/hlc"
	"github.com/rachitkumar205/acp-kv/internal/metrics"
	"github.com/rachitkumar205/acp-kv/internal/storage"
)

// shared metrics instance to avoid duplicate registration
var testMetrics = metrics.NewMetrics("test")

func TestDetector_IsStale(t *testing.T) {
	detector := NewDetector(3*time.Second, testMetrics)

	now := time.Now().UnixNano()

	tests := []struct {
		name      string
		timestamp hlc.HLC
		expected  bool
	}{
		{
			name:      "fresh data (1s old)",
			timestamp: hlc.HLC{Physical: now - int64(1*time.Second), Logical: 0, NodeID: "node1"},
			expected:  false,
		},
		{
			name:      "borderline fresh (2.9s old)",
			timestamp: hlc.HLC{Physical: now - int64(2900*time.Millisecond), Logical: 0, NodeID: "node1"},
			expected:  false,
		},
		{
			name:      "stale data (4s old)",
			timestamp: hlc.HLC{Physical: now - int64(4*time.Second), Logical: 0, NodeID: "node1"},
			expected:  true,
		},
		{
			name:      "very stale data (10s old)",
			timestamp: hlc.HLC{Physical: now - int64(10*time.Second), Logical: 0, NodeID: "node1"},
			expected:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isStale := detector.IsStale(tt.timestamp, now)
			if isStale != tt.expected {
				t.Errorf("expected stale=%v, got %v", tt.expected, isStale)
			}
		})
	}
}

func TestDetector_CheckStrict(t *testing.T) {
	detector := NewDetector(3*time.Second, testMetrics)

	now := time.Now().UnixNano()

	// fresh value should pass
	freshValue := storage.VersionedValue{
		Value:   []byte("fresh"),
		Version: now,
		HLC:     hlc.HLC{Physical: now - int64(1*time.Second), Logical: 0, NodeID: "node1"},
	}

	err := detector.CheckStrict(freshValue)
	if err != nil {
		t.Errorf("expected no error for fresh value, got %v", err)
	}

	// stale value should fail
	staleValue := storage.VersionedValue{
		Value:   []byte("stale"),
		Version: now,
		HLC:     hlc.HLC{Physical: now - int64(5*time.Second), Logical: 0, NodeID: "node1"},
	}

	err = detector.CheckStrict(staleValue)
	if err == nil {
		t.Error("expected error for stale value")
	}
}

func TestDetector_CheckMultiple(t *testing.T) {
	detector := NewDetector(3*time.Second, testMetrics)

	now := time.Now().UnixNano()

	values := []storage.VersionedValue{
		{
			Value:   []byte("fresh1"),
			HLC:     hlc.HLC{Physical: now - int64(1*time.Second), Logical: 0, NodeID: "node1"},
		},
		{
			Value:   []byte("stale1"),
			HLC:     hlc.HLC{Physical: now - int64(5*time.Second), Logical: 0, NodeID: "node2"},
		},
		{
			Value:   []byte("fresh2"),
			HLC:     hlc.HLC{Physical: now - int64(2*time.Second), Logical: 0, NodeID: "node3"},
		},
		{
			Value:   []byte("stale2"),
			HLC:     hlc.HLC{Physical: now - int64(10*time.Second), Logical: 0, NodeID: "node4"},
		},
	}

	fresh, stale := detector.CheckMultiple(values)

	if len(fresh) != 2 {
		t.Errorf("expected 2 fresh values, got %d", len(fresh))
	}

	if len(stale) != 2 {
		t.Errorf("expected 2 stale values, got %d", len(stale))
	}
}

func TestDetector_Age(t *testing.T) {
	detector := NewDetector(3*time.Second, testMetrics)

	now := time.Now().UnixNano()
	timestamp := hlc.HLC{Physical: now - int64(5*time.Second), Logical: 0, NodeID: "node1"}

	age := detector.Age(timestamp, now)

	if age < 4*time.Second || age > 6*time.Second {
		t.Errorf("expected age ~5s, got %v", age)
	}
}
