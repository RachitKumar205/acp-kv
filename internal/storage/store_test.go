package storage

import (
	"sync"
	"testing"
	"time"
)

func TestStore_PutAndGet(t *testing.T) {
	store := NewStore()
	key := "test-key"
	value := []byte("test-value")
	nodeID := "node1"

	vv := store.Put(key, value, nodeID)

	// verify
	if vv.NodeID != nodeID {
		t.Errorf("expected node_id %s, got %s", nodeID, vv.NodeID)
	}
	if string(vv.Value) != string(value) {
		t.Errorf("expected value %s, got %s", value, vv.Value)
	}

	// get the value
	retrieved, found := store.Get(key)
	if !found {
		t.Fatal("expected to find key")
	}

	if string(retrieved.Value) != string(value) {
		t.Errorf("expected value %s, got %s", value, retrieved.Value)
	}

	if retrieved.Version != vv.Version {
		t.Errorf("expected version %d, got %d", vv.Version, retrieved.Version)
	}
}

func TestStore_GetNonExistent(t *testing.T) {
	store := NewStore()
	_, found := store.Get("non-existent")
	if found {
		t.Error("expected not to find non existent key")
	}
}

func TestStore_Overwrite(t *testing.T) {
	store := NewStore()
	key := "test-key"
	value1 := []byte("value1")
	value2 := []byte("value2")
	nodeID := "node1"

	vv1 := store.Put(key, value1, nodeID)
	time.Sleep(1 * time.Millisecond) // ensure timestamp difference

	vv2 := store.Put(key, value2, nodeID)

	if vv2.Version <= vv1.Version {
		t.Error("expected second version to be greater than first")
	}

	retrieved, found := store.Get(key)
	if !found {
		t.Fatal("expected to find key")
	}
	if string(retrieved.Value) != string(value2) {
		t.Errorf("expected value %s, got %s", value2, retrieved.Value)
	}
}

func TestStore_ConcurrentWrites(t *testing.T) {
	store := NewStore()
	numGoroutines := 100
	var wg sync.WaitGroup

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			key := string(rune('a' + id%26))
			value := []byte{byte(id)}
			store.Put(key, value, "node1")
		}(i)

		wg.Wait()

		if store.Size() > 26 {
			t.Errorf("expected at most 26 keys, got %d", store.Size())
		}
	}
}

func TestStore_ConcurrentReads(t *testing.T) {
	store := NewStore()
	key := "test-key"
	value := []byte("test-value")
	store.Put(key, value, "node1")

	numGoroutines := 100
	var wg sync.WaitGroup
	errors := make(chan error, numGoroutines)

	// concurrent reads
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			retrieved, found := store.Get(key)
			if !found {
				errors <- nil
				return
			}
			if string(retrieved.Value) != string(value) {
				errors <- nil
			}
		}()
	}

	wg.Wait()
	close(errors)

	for range errors {
		t.Error("concurrent read failed")
	}
}

func TestStore_GetAll(t *testing.T) {
	store := NewStore()
	key := "test-key"
	value := []byte("test-value")
	nodeID := "node1"

	store.Put(key, value, nodeID)

	versions := store.GetAll(key)
	if len(versions) != 1 {
		t.Errorf("expected 1 version, got %d", len(versions))
	}
	if string(versions[0].Value) != string(value) {
		t.Errorf("expected value %s, got %s", value, versions[0].Value)
	}
}

func TestStore_GetAllNonExistent(t *testing.T) {
	store := NewStore()
	versions := store.GetAll("non-existent")
	if len(versions) != 0 {
		t.Errorf("expected 0 versions, got %d", len(versions))
	}
}

func TestStore_Size(t *testing.T) {
	store := NewStore()

	if store.Size() != 0 {
		t.Errorf("expected size 0, got %d", store.Size())
	}

	store.Put("key1", []byte("value1"), "node1")
	if store.Size() != 1 {
		t.Errorf("expected size 1, got %d", store.Size())
	}

	store.Put("key2", []byte("value2"), "node1")
	if store.Size() != 2 {
		t.Errorf("expected size 2, got %d", store.Size())
	}

	// overwriting should not change size
	store.Put("key1", []byte("new-value"), "node1")
	if store.Size() != 2 {
		t.Errorf("expected size 2, got %d", store.Size())
	}
}
