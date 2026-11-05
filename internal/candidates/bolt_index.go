package candidates

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	bolt "go.etcd.io/bbolt"

	"dispatchkernel/internal/kernel"
	"dispatchkernel/internal/spatial"
)

var candidateBucket = []byte("candidates")

type BoltIndex struct {
	mu    sync.RWMutex
	db    *bolt.DB
	index *spatial.H3Index
}

func OpenBoltIndex(path string, resolution, radius int) (*BoltIndex, error) {
	if path == "" {
		return nil, fmt.Errorf("candidate database path is required")
	}
	index, err := spatial.NewH3Index(resolution, radius)
	if err != nil {
		return nil, err
	}
	db, err := bolt.Open(path, 0600, &bolt.Options{Timeout: time.Second})
	if err != nil {
		return nil, fmt.Errorf("open candidate database: %w", err)
	}
	store := &BoltIndex{db: db, index: index}
	if err := store.load(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (store *BoltIndex) load() error {
	return store.db.Update(func(tx *bolt.Tx) error {
		bucket, err := tx.CreateBucketIfNotExists(candidateBucket)
		if err != nil {
			return fmt.Errorf("create candidate bucket: %w", err)
		}
		return bucket.ForEach(func(key, value []byte) error {
			var candidate kernel.Candidate
			if err := json.Unmarshal(value, &candidate); err != nil {
				return fmt.Errorf("decode candidate %q: %w", key, err)
			}
			if candidate.ID != string(key) {
				return fmt.Errorf("candidate key %q does not match stored id %q", key, candidate.ID)
			}
			if err := store.index.Upsert(candidate); err != nil {
				return fmt.Errorf("restore candidate %q: %w", key, err)
			}
			return nil
		})
	})
}

func (store *BoltIndex) Upsert(candidate kernel.Candidate) error {
	if err := candidate.Validate(); err != nil {
		return err
	}
	value, err := json.Marshal(candidate)
	if err != nil {
		return fmt.Errorf("encode candidate %q: %w", candidate.ID, err)
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	if err := store.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(candidateBucket)
		if bucket == nil {
			return fmt.Errorf("candidate bucket is missing")
		}
		return bucket.Put([]byte(candidate.ID), value)
	}); err != nil {
		return fmt.Errorf("persist candidate %q: %w", candidate.ID, err)
	}
	if err := store.index.Upsert(candidate); err != nil {
		return fmt.Errorf("index persisted candidate %q: %w", candidate.ID, err)
	}
	return nil
}

func (store *BoltIndex) Delete(id string) (bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	deleted := false
	err := store.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(candidateBucket)
		if bucket == nil {
			return fmt.Errorf("candidate bucket is missing")
		}
		if bucket.Get([]byte(id)) == nil {
			return nil
		}
		if err := bucket.Delete([]byte(id)); err != nil {
			return err
		}
		deleted = true
		return nil
	})
	if err != nil {
		return false, fmt.Errorf("delete candidate %q: %w", id, err)
	}
	if !deleted {
		return false, nil
	}
	if !store.index.Delete(id) {
		return false, fmt.Errorf("candidate %q was persisted but missing from spatial index", id)
	}
	return true, nil
}

func (store *BoltIndex) Search(ctx context.Context, around kernel.Location, limit int) ([]kernel.Candidate, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	return store.index.Search(ctx, around, limit)
}

func (store *BoltIndex) Close() error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if err := store.db.Close(); err != nil {
		return fmt.Errorf("close candidate database: %w", err)
	}
	return nil
}

func EnsureParentDirectory(path string) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0700); err != nil {
		return fmt.Errorf("create candidate database directory: %w", err)
	}
	return nil
}
