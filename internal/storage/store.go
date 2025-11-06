package storage

import (
	"sync"
	"time"
)

type VersionedValue struct {
	Value     []byte
	Version   int64
	Timestamp int64
	NodeID    string
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
