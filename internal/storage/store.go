package storage

import (
	"sync"
	"time"

	"github.com/rachitkumar205/acp-kv/internal/hlc"
)

type VersionedValue struct {
	Value      []byte
	Version    int64
	Timestamp  int64    // deprecated, kept for backward compatibility
	NodeID     string
	HLC        hlc.HLC  // hybrid logical clock timestamp
	ReceivedAt int64    // local time when value was received
	IsLocal    bool     // true if originated on this node
}

// thread safe in-memory kv store
type Store struct {
	mu   sync.RWMutex
	data map[string]VersionedValue
}

// create new store instance
func NewStore() *Store {
	return &Store{
		data: make(map[string]VersionedValue),
	}
}

// put kv pair with version and timestamp
func (s *Store) Put(key string, value []byte, nodeID string) VersionedValue {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UnixNano()
	version := now

	vv := VersionedValue{
		Value:     value,
		Version:   version,
		Timestamp: now,
		NodeID:    nodeID,
	}

	s.data[key] = vv
	return vv
}

// retrieve value by key
func (s *Store) Get(key string) (VersionedValue, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	vv, exists := s.data[key]
	return vv, exists
}

// returns all values for a key (future impl)
// return single value for now
func (s *Store) GetAll(key string) []VersionedValue {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if vv, exists := s.data[key]; exists {
		return []VersionedValue{vv}
	}

	return []VersionedValue{}
}

// returns number of keys in the store
func (s *Store) Size() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.data)
}

// put kv pair with hlc timestamp
func (s *Store) PutWithHLC(key string, value []byte, nodeID string, timestamp hlc.HLC) VersionedValue {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UnixNano()

	vv := VersionedValue{
		Value:      value,
		Version:    timestamp.Physical,  // use hlc physical as version
		Timestamp:  timestamp.Physical,  // backward compatibility
		NodeID:     nodeID,
		HLC:        timestamp,
		ReceivedAt: now,
		IsLocal:    nodeID == timestamp.NodeID,
	}

	s.data[key] = vv
	return vv
}

// retrieve value with staleness check
// returns (value, found, isStale)
func (s *Store) GetWithStaleness(key string, maxAge time.Duration) (VersionedValue, bool, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	vv, exists := s.data[key]
	if !exists {
		return VersionedValue{}, false, false
	}

	// check if value is too stale
	now := time.Now().UnixNano()
	age := vv.HLC.Age(now)
	isStale := age > maxAge

	return vv, true, isStale
}

// detect conflicting values (concurrent writes)
// for now returns single value as we implement LWW
func (s *Store) GetConflicts(key string) []VersionedValue {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if vv, exists := s.data[key]; exists {
		return []VersionedValue{vv}
	}
	return []VersionedValue{}
}
