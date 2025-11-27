package replication

import (
	"testing"

	"github.com/rachitkumar205/acp-kv/internal/hlc"
)

func TestGetMostRecent(t *testing.T) {
	tests := []struct {
		name     string
		values   []ReplicaValue
		expected ReplicaValue
		found    bool
	}{
		{
			name:     "empty list",
			values:   []ReplicaValue{},
			expected: ReplicaValue{},
			found:    false,
		},
		{
			name: "single value",
			values: []ReplicaValue{
				{PeerAddr: "node1", Value: []byte("v1"), Timestamp: 100, HLC: hlc.HLC{Physical: 100, Logical: 0, NodeID: "node1"}},
			},
			expected: ReplicaValue{PeerAddr: "node1", Value: []byte("v1"), Timestamp: 100, HLC: hlc.HLC{Physical: 100, Logical: 0, NodeID: "node1"}},
			found:    true,
		},
		{
			name: "multiple values - most recent last",
			values: []ReplicaValue{
				{PeerAddr: "node1", Value: []byte("v1"), Timestamp: 100, HLC: hlc.HLC{Physical: 100, Logical: 0, NodeID: "node1"}},
				{PeerAddr: "node2", Value: []byte("v2"), Timestamp: 200, HLC: hlc.HLC{Physical: 200, Logical: 0, NodeID: "node2"}},
				{PeerAddr: "node3", Value: []byte("v3"), Timestamp: 300, HLC: hlc.HLC{Physical: 300, Logical: 0, NodeID: "node3"}},
			},
			expected: ReplicaValue{PeerAddr: "node3", Value: []byte("v3"), Timestamp: 300, HLC: hlc.HLC{Physical: 300, Logical: 0, NodeID: "node3"}},
			found:    true,
		},
		{
			name: "multiple values - most recent first",
			values: []ReplicaValue{
				{PeerAddr: "node1", Value: []byte("v1"), Timestamp: 300, HLC: hlc.HLC{Physical: 300, Logical: 0, NodeID: "node1"}},
				{PeerAddr: "node2", Value: []byte("v2"), Timestamp: 200, HLC: hlc.HLC{Physical: 200, Logical: 0, NodeID: "node2"}},
				{PeerAddr: "node3", Value: []byte("v3"), Timestamp: 100, HLC: hlc.HLC{Physical: 100, Logical: 0, NodeID: "node3"}},
			},
			expected: ReplicaValue{PeerAddr: "node1", Value: []byte("v1"), Timestamp: 300, HLC: hlc.HLC{Physical: 300, Logical: 0, NodeID: "node1"}},
			found:    true,
		},
		{
			name: "multiple values - most recent middle",
			values: []ReplicaValue{
				{PeerAddr: "node1", Value: []byte("v1"), Timestamp: 100, HLC: hlc.HLC{Physical: 100, Logical: 0, NodeID: "node1"}},
				{PeerAddr: "node2", Value: []byte("v2"), Timestamp: 300, HLC: hlc.HLC{Physical: 300, Logical: 0, NodeID: "node2"}},
				{PeerAddr: "node3", Value: []byte("v3"), Timestamp: 200, HLC: hlc.HLC{Physical: 200, Logical: 0, NodeID: "node3"}},
			},
			expected: ReplicaValue{PeerAddr: "node2", Value: []byte("v2"), Timestamp: 300, HLC: hlc.HLC{Physical: 300, Logical: 0, NodeID: "node2"}},
			found:    true,
		},
		{
			name: "same timestamps",
			values: []ReplicaValue{
				{PeerAddr: "node1", Value: []byte("v1"), Timestamp: 100, HLC: hlc.HLC{Physical: 100, Logical: 0, NodeID: "node1"}},
				{PeerAddr: "node2", Value: []byte("v2"), Timestamp: 100, HLC: hlc.HLC{Physical: 100, Logical: 0, NodeID: "node2"}},
			},
			expected: ReplicaValue{PeerAddr: "node1", Value: []byte("v1"), Timestamp: 100, HLC: hlc.HLC{Physical: 100, Logical: 0, NodeID: "node1"}},
			found:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, found := GetMostRecent(tt.values)
			if found != tt.found {
				t.Errorf("expected found=%v, got found=%v", tt.found, found)
			}
			if found && result.Timestamp != tt.expected.Timestamp {
				t.Errorf("expected timestamp=%d, got timestamp=%d", tt.expected.Timestamp, result.Timestamp)
			}
			if found && string(result.Value) != string(tt.expected.Value) {
				t.Errorf("expected value=%s, got value=%s", tt.expected.Value, result.Value)
			}
		})
	}
}
