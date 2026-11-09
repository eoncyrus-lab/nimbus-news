package event

import (
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"

	bolt "go.etcd.io/bbolt"
)

const (
	stateBucket    = "event_state"
	storyBucket    = "event_story"
	threadBucket   = "event_thread"
	decisionBucket = "event_decision"
	statBucket     = "event_source_stat"
)

// Item 是 List 返回的键值对。
type Item struct {
	Key   string
	Value []byte
}

// Store 是事件模型的最小持久化接口。value 自带过期时间，读时惰性过滤。
type Store interface {
	Put(bucket, key string, value []byte, ttl time.Duration) error
	Get(bucket, key string) ([]byte, bool, error)
	List(bucket, prefix string) ([]Item, error)
	Delete(bucket, key string) error
}

// PutState 把任意结构以 JSON 写入。
func PutState(s Store, bucket, key string, value interface{}, ttl time.Duration) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return s.Put(bucket, key, data, ttl)
}

// GetState 读取 JSON 结构。
func GetState(s Store, bucket, key string, dst interface{}) (bool, error) {
	data, ok, err := s.Get(bucket, key)
	if err != nil || !ok {
		return false, err
	}
	if err := json.Unmarshal(data, dst); err != nil {
		return false, err
	}
	return true, nil
}

type memEntry struct {
	value   []byte
	expires time.Time
}

// MemoryStore 用于测试与无 Bolt 的降级运行。
type MemoryStore struct {
	mu      sync.RWMutex
	buckets map[string]map[string]memEntry
	now     func() time.Time
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{buckets: make(map[string]map[string]memEntry), now: time.Now}
}

func (s *MemoryStore) Put(bucket, key string, value []byte, ttl time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.buckets[bucket] == nil {
		s.buckets[bucket] = make(map[string]memEntry)
	}
	copied := append([]byte(nil), value...)
	s.buckets[bucket][key] = memEntry{value: copied, expires: s.now().Add(ttl)}
	return nil
}

func (s *MemoryStore) Get(bucket, key string) ([]byte, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	entry, ok := s.buckets[bucket][key]
	if !ok || (!entry.expires.IsZero() && !entry.expires.After(s.now())) {
		return nil, false, nil
	}
	return append([]byte(nil), entry.value...), true, nil
}

func (s *MemoryStore) List(bucket, prefix string) ([]Item, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []Item
	for key, entry := range s.buckets[bucket] {
		if prefix != "" && len(key) < len(prefix) || (prefix != "" && key[:len(prefix)] != prefix) {
			continue
		}
		if !entry.expires.IsZero() && !entry.expires.After(s.now()) {
			continue
		}
		out = append(out, Item{Key: key, Value: append([]byte(nil), entry.value...)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

func (s *MemoryStore) Delete(bucket, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.buckets[bucket], key)
	return nil
}

type boltEnvelope struct {
	Expires int64           `json:"e"`
	Value   json.RawMessage `json:"v"`
}

// BoltEventStore 复用平台 BoltDB 文件，单独开 bucket。
type BoltEventStore struct {
	db *bolt.DB
}

func NewBoltEventStore(db *bolt.DB) *BoltEventStore {
	return &BoltEventStore{db: db}
}

func (s *BoltEventStore) Put(bucket, key string, value []byte, ttl time.Duration) error {
	envelope, err := json.Marshal(boltEnvelope{
		Expires: time.Now().Add(ttl).Unix(),
		Value:   append(json.RawMessage(nil), value...),
	})
	if err != nil {
		return err
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte(bucket))
		if err != nil {
			return err
		}
		return b.Put([]byte(key), envelope)
	})
}

func (s *BoltEventStore) Get(bucket, key string) ([]byte, bool, error) {
	var out []byte
	found := false
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucket))
		if b == nil {
			return nil
		}
		raw := b.Get([]byte(key))
		if raw == nil {
			return nil
		}
		var envelope boltEnvelope
		if err := json.Unmarshal(raw, &envelope); err != nil {
			return fmt.Errorf("event store decode: %w", err)
		}
		if envelope.Expires > 0 && envelope.Expires <= time.Now().Unix() {
			return nil
		}
		out = append([]byte(nil), envelope.Value...)
		found = true
		return nil
	})
	return out, found, err
}

func (s *BoltEventStore) List(bucket, prefix string) ([]Item, error) {
	var out []Item
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucket))
		if b == nil {
			return nil
		}
		cursor := b.Cursor()
		start := []byte(prefix)
		for k, v := cursor.Seek(start); k != nil && (prefix == "" || hasPrefixBytes(k, start)); k, v = cursor.Next() {
			var envelope boltEnvelope
			if err := json.Unmarshal(v, &envelope); err != nil {
				continue
			}
			if envelope.Expires > 0 && envelope.Expires <= time.Now().Unix() {
				continue
			}
			out = append(out, Item{Key: string(k), Value: append([]byte(nil), envelope.Value...)})
		}
		return nil
	})
	return out, err
}

func (s *BoltEventStore) Delete(bucket, key string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucket))
		if b == nil {
			return nil
		}
		return b.Delete([]byte(key))
	})
}

func hasPrefixBytes(b, prefix []byte) bool {
	if len(b) < len(prefix) {
		return false
	}
	for i := range prefix {
		if b[i] != prefix[i] {
			return false
		}
	}
	return true
}
