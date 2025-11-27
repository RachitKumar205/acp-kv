package reconcile

import (
	"testing"
	"time"

	"github.com/rachitkumar205/acp-kv/internal/hlc"
	"github.com/rachitkumar205/acp-kv/internal/metrics"
	"github.com/rachitkumar205/acp-kv/internal/storage"
	"go.uber.org/zap"
)

// shared metrics instance to avoid duplicate registration
var testMetrics = metrics.NewMetrics("test")

type mockCoordinator struct {
	peers []string
}

func (m *mockCoordinator) GetPeerAddresses() []string {
	return m.peers
}

func TestEngine_RecordWrite(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	store := storage.NewStore()
	coord := &mockCoordinator{peers: []string{"peer1", "peer2"}}

	engine := NewEngine(store, coord, time.Second, true, logger, testMetrics)

	// record a write
	timestamp := hlc.HLC{Physical: time.Now().UnixNano(), Logical: 0, NodeID: "node1"}
	engine.RecordWrite("key1", []byte("value1"), "node1", timestamp)

	// verify it's in the log
	writes := engine.recentWrites.GetAll()
	if len(writes) != 1 {
		t.Errorf("expected 1 write in log, got %d", len(writes))
	}

	if writes[0].Key != "key1" {
		t.Errorf("expected key=key1, got %s", writes[0].Key)
	}
}

func TestEngine_ReconcileWithPeer_LWW(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	store := storage.NewStore()
	coord := &mockCoordinator{peers: []string{"peer1"}}

	engine := NewEngine(store, coord, time.Second, true, logger, testMetrics)

	now := time.Now().UnixNano()

	// local value (older)
	localTimestamp := hlc.HLC{Physical: now - int64(1*time.Second), Logical: 0, NodeID: "node1"}
	store.PutWithHLC("key1", []byte("local_value"), "node1", localTimestamp)

	// remote write (newer)
	remoteTimestamp := hlc.HLC{Physical: now, Logical: 0, NodeID: "peer1"}
	engine.RecordWrite("key1", []byte("remote_value"), "peer1", remoteTimestamp)

	// reconcile
	engine.reconcileWithPeer("peer1")

	// local store should now have the newer remote value
	value, found := store.Get("key1")
	if !found {
		t.Fatal("expected key1 to exist after reconciliation")
	}

	if string(value.Value) != "remote_value" {
		t.Errorf("expected remote_value, got %s", string(value.Value))
	}

	if value.NodeID != "peer1" {
		t.Errorf("expected nodeID=peer1, got %s", value.NodeID)
	}
}

func TestEngine_ReconcileWithPeer_LocalNewer(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	store := storage.NewStore()
	coord := &mockCoordinator{peers: []string{"peer1"}}

	engine := NewEngine(store, coord, time.Second, true, logger, testMetrics)

	now := time.Now().UnixNano()

	// local value (newer)
	localTimestamp := hlc.HLC{Physical: now, Logical: 0, NodeID: "node1"}
	store.PutWithHLC("key1", []byte("local_value"), "node1", localTimestamp)

	// remote write (older)
	remoteTimestamp := hlc.HLC{Physical: now - int64(1*time.Second), Logical: 0, NodeID: "peer1"}
	engine.RecordWrite("key1", []byte("remote_value"), "peer1", remoteTimestamp)

	// reconcile
	engine.reconcileWithPeer("peer1")

	// local store should keep the local value (newer)
	value, found := store.Get("key1")
	if !found {
		t.Fatal("expected key1 to exist after reconciliation")
	}

	if string(value.Value) != "local_value" {
		t.Errorf("expected local_value, got %s", string(value.Value))
	}
}

func TestEngine_ReconcileWithPeer_ConcurrentWrites(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	store := storage.NewStore()
	coord := &mockCoordinator{peers: []string{"peer1"}}

	engine := NewEngine(store, coord, time.Second, true, logger, testMetrics)

	now := time.Now().UnixNano()

	// concurrent writes with same hlc timestamp
	timestamp := hlc.HLC{Physical: now, Logical: 0, NodeID: "node1"}
	store.PutWithHLC("key1", []byte("local_value"), "node1", timestamp)

	// remote write with same timestamp but different node
	remoteTimestamp := hlc.HLC{Physical: now, Logical: 0, NodeID: "peer1"}
	engine.RecordWrite("key1", []byte("remote_value"), "peer1", remoteTimestamp)

	// reconcile - should use nodeID as tiebreaker
	engine.reconcileWithPeer("peer1")

	value, found := store.Get("key1")
	if !found {
		t.Fatal("expected key1 to exist after reconciliation")
	}

	// peer1 > node1 lexicographically, so remote should win
	if string(value.Value) != "remote_value" {
		t.Errorf("expected remote_value to win tiebreak, got %s", string(value.Value))
	}
}

func TestRecentWriteLog_AddAndGet(t *testing.T) {
	log := NewRecentWriteLog(10, 5*time.Minute)

	timestamp := hlc.HLC{Physical: time.Now().UnixNano(), Logical: 0, NodeID: "node1"}
	log.Add("key1", []byte("value1"), "node1", timestamp)

	writes := log.GetAll()
	if len(writes) != 1 {
		t.Errorf("expected 1 write, got %d", len(writes))
	}

	if writes[0].Key != "key1" {
		t.Errorf("expected key=key1, got %s", writes[0].Key)
	}
}

func TestRecentWriteLog_CircularBuffer(t *testing.T) {
	log := NewRecentWriteLog(3, 5*time.Minute) // max 3 entries

	now := time.Now().UnixNano()

	// add 5 writes (more than capacity)
	for i := 0; i < 5; i++ {
		timestamp := hlc.HLC{Physical: now, Logical: int64(i), NodeID: "node1"}
		log.Add("key", []byte("value"), "node1", timestamp)
	}

	// should only keep last 3
	if log.Size() != 3 {
		t.Errorf("expected size=3, got %d", log.Size())
	}
}

func TestRecentWriteLog_Expiration(t *testing.T) {
	log := NewRecentWriteLog(10, 100*time.Millisecond) // expire after 100ms

	// add old write
	oldTimestamp := hlc.HLC{Physical: time.Now().UnixNano(), Logical: 0, NodeID: "node1"}
	log.Add("old_key", []byte("old_value"), "node1", oldTimestamp)

	// wait to ensure old write expires
	time.Sleep(150 * time.Millisecond)

	// add fresh write
	freshTimestamp := hlc.HLC{Physical: time.Now().UnixNano(), Logical: 0, NodeID: "node1"}
	log.Add("fresh_key", []byte("fresh_value"), "node1", freshTimestamp)

	// get all should filter expired
	writes := log.GetAll()
	if len(writes) != 1 {
		t.Errorf("expected 1 fresh write, got %d", len(writes))
	}

	if writes[0].Key != "fresh_key" {
		t.Errorf("expected fresh_key, got %s", writes[0].Key)
	}
}
