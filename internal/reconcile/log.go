package reconcile

import (
	"sync"
	"time"

	"github.com/rachitkumar205/acp-kv/internal/hlc"
)

// writeentry represents a single write operation
type WriteEntry struct {
	Key       string
	Value     []byte
	NodeID    string
	HLC       hlc.HLC
	Timestamp int64 // local receipt time
}

// recentwritelog maintains a circular buffer of recent writes for reconciliation
type RecentWriteLog struct {
	mu         sync.RWMutex
	entries    []WriteEntry
	maxSize    int
	index      int
	count      int
	maxAge     time.Duration
	timestamps []int64 // corresponding timestamps for entries
}

// newrecentwritelog creates a new recent write log
func NewRecentWriteLog(maxSize int, maxAge time.Duration) *RecentWriteLog {
	return &RecentWriteLog{
		entries:    make([]WriteEntry, maxSize),
		timestamps: make([]int64, maxSize),
		maxSize:    maxSize,
		maxAge:     maxAge,
	}
}

// add inserts a write into the log
func (rwl *RecentWriteLog) Add(key string, value []byte, nodeID string, timestamp hlc.HLC) {
	rwl.mu.Lock()
	defer rwl.mu.Unlock()

	now := time.Now().UnixNano()

	entry := WriteEntry{
		Key:       key,
		Value:     value,
		NodeID:    nodeID,
		HLC:       timestamp,
		Timestamp: now,
	}

	rwl.entries[rwl.index] = entry
	rwl.timestamps[rwl.index] = now
	rwl.index = (rwl.index + 1) % rwl.maxSize
	if rwl.count < rwl.maxSize {
		rwl.count++
	}
}

// getall returns all non-expired writes from the log
func (rwl *RecentWriteLog) GetAll() []WriteEntry {
	rwl.mu.RLock()
	defer rwl.mu.RUnlock()

	now := time.Now().UnixNano()
	cutoff := now - int64(rwl.maxAge)

	result := make([]WriteEntry, 0, rwl.count)
	for i := 0; i < rwl.count; i++ {
		if rwl.timestamps[i] >= cutoff {
			result = append(result, rwl.entries[i])
		}
	}

	return result
}

// size returns the current number of entries in the log
func (rwl *RecentWriteLog) Size() int {
	rwl.mu.RLock()
	defer rwl.mu.RUnlock()
	return rwl.count
}

// cleanup removes expired entries (called periodically)
func (rwl *RecentWriteLog) Cleanup() {
	rwl.mu.Lock()
	defer rwl.mu.Unlock()

	now := time.Now().UnixNano()
	cutoff := now - int64(rwl.maxAge)

	validCount := 0
	newEntries := make([]WriteEntry, rwl.maxSize)
	newTimestamps := make([]int64, rwl.maxSize)

	for i := 0; i < rwl.count; i++ {
		if rwl.timestamps[i] >= cutoff {
			newEntries[validCount] = rwl.entries[i]
			newTimestamps[validCount] = rwl.timestamps[i]
			validCount++
		}
	}

	rwl.entries = newEntries
	rwl.timestamps = newTimestamps
	rwl.count = validCount
	rwl.index = validCount % rwl.maxSize
}
