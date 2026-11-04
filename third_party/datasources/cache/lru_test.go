package cache

import (
	"testing"
	"time"
)

func TestLRUCache_SetGet(t *testing.T) {
	cache := NewLRUCache(10, time.Minute)

	cache.Set("key1", "value1")
	cache.Set("key2", "value2")

	val, ok := cache.Get("key1")
	if !ok {
		t.Error("expected key1 to exist")
	}
	if val != "value1" {
		t.Errorf("expected value1, got %v", val)
	}

	val, ok = cache.Get("key2")
	if !ok {
		t.Error("expected key2 to exist")
	}
	if val != "value2" {
		t.Errorf("expected value2, got %v", val)
	}
}

func TestLRUCache_Eviction(t *testing.T) {
	cache := NewLRUCache(3, time.Minute)

	cache.Set("key1", "value1")
	cache.Set("key2", "value2")
	cache.Set("key3", "value3")
	cache.Set("key4", "value4") // 应该驱逐 key1

	if cache.Len() != 3 {
		t.Errorf("expected len 3, got %d", cache.Len())
	}

	_, ok := cache.Get("key1")
	if ok {
		t.Error("expected key1 to be evicted")
	}

	_, ok = cache.Get("key4")
	if !ok {
		t.Error("expected key4 to exist")
	}
}

func TestLRUCache_LRUOrder(t *testing.T) {
	cache := NewLRUCache(3, time.Minute)

	cache.Set("key1", "value1")
	cache.Set("key2", "value2")
	cache.Set("key3", "value3")

	// 访问 key1，使其成为最近使用
	cache.Get("key1")

	// 添加 key4，应该驱逐 key2（最久未使用）
	cache.Set("key4", "value4")

	_, ok := cache.Get("key1")
	if !ok {
		t.Error("expected key1 to exist (was recently accessed)")
	}

	_, ok = cache.Get("key2")
	if ok {
		t.Error("expected key2 to be evicted")
	}
}

func TestLRUCache_Expiration(t *testing.T) {
	cache := NewLRUCache(10, 50*time.Millisecond)

	cache.Set("key1", "value1")

	// 立即获取应该成功
	_, ok := cache.Get("key1")
	if !ok {
		t.Error("expected key1 to exist")
	}

	// 等待过期
	time.Sleep(100 * time.Millisecond)

	// 过期后获取应该失败
	_, ok = cache.Get("key1")
	if ok {
		t.Error("expected key1 to be expired")
	}
}

func TestLRUCache_Delete(t *testing.T) {
	cache := NewLRUCache(10, time.Minute)

	cache.Set("key1", "value1")
	cache.Delete("key1")

	_, ok := cache.Get("key1")
	if ok {
		t.Error("expected key1 to be deleted")
	}
}

func TestLRUCache_Has(t *testing.T) {
	cache := NewLRUCache(10, time.Minute)

	if cache.Has("key1") {
		t.Error("expected key1 to not exist")
	}

	cache.Set("key1", "value1")

	if !cache.Has("key1") {
		t.Error("expected key1 to exist")
	}
}

func TestLRUCache_Clear(t *testing.T) {
	cache := NewLRUCache(10, time.Minute)

	cache.Set("key1", "value1")
	cache.Set("key2", "value2")
	cache.Clear()

	if cache.Len() != 0 {
		t.Errorf("expected len 0 after clear, got %d", cache.Len())
	}
}

func TestLRUCache_GetOrSet(t *testing.T) {
	cache := NewLRUCache(10, time.Minute)
	callCount := 0

	fn := func() (interface{}, error) {
		callCount++
		return "computed_value", nil
	}

	// 第一次调用，应该调用 fn
	val, err := cache.GetOrSet("key1", fn)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if val != "computed_value" {
		t.Errorf("expected computed_value, got %v", val)
	}
	if callCount != 1 {
		t.Errorf("expected fn to be called once, called %d times", callCount)
	}

	// 第二次调用，应该从缓存获取，不调用 fn
	val, err = cache.GetOrSet("key1", fn)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if val != "computed_value" {
		t.Errorf("expected computed_value, got %v", val)
	}
	if callCount != 1 {
		t.Errorf("expected fn to still be called only once, called %d times", callCount)
	}
}

func TestLRUCache_Stats(t *testing.T) {
	cache := NewLRUCache(10, time.Minute)

	cache.Set("key1", "value1")
	cache.Get("key1") // hit
	cache.Get("key2") // miss

	stats := cache.Stats()
	if stats.Size != 1 {
		t.Errorf("expected size 1, got %d", stats.Size)
	}
	if stats.Hits != 1 {
		t.Errorf("expected 1 hit, got %d", stats.Hits)
	}
	if stats.Misses != 1 {
		t.Errorf("expected 1 miss, got %d", stats.Misses)
	}
}

func BenchmarkLRUCache_Set(b *testing.B) {
	cache := NewLRUCache(1000, time.Minute)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cache.Set("key", "value")
	}
}

func BenchmarkLRUCache_Get(b *testing.B) {
	cache := NewLRUCache(1000, time.Minute)
	cache.Set("key", "value")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cache.Get("key")
	}
}
