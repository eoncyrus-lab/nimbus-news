// Package cache 提供缓存实现
package cache

import (
	"container/list"
	"sync"
	"time"
)

// LRUCache LRU 缓存
type LRUCache struct {
	capacity int
	ttl      time.Duration
	items    map[string]*list.Element
	order    *list.List
	mu       sync.RWMutex

	// 统计信息
	hits   int64
	misses int64
}

type cacheEntry struct {
	key       string
	value     interface{}
	expiresAt time.Time
}

// NewLRUCache 创建 LRU 缓存
func NewLRUCache(capacity int, ttl time.Duration) *LRUCache {
	c := &LRUCache{
		capacity: capacity,
		ttl:      ttl,
		items:    make(map[string]*list.Element),
		order:    list.New(),
	}

	// 启动清理协程
	go c.cleanupLoop()

	return c
}

// Get 获取缓存值
func (c *LRUCache) Get(key string) (interface{}, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	elem, ok := c.items[key]
	if !ok {
		c.misses++
		return nil, false
	}

	entry := elem.Value.(*cacheEntry)

	// 检查是否过期
	if time.Now().After(entry.expiresAt) {
		c.removeElement(elem)
		c.misses++
		return nil, false
	}

	// 移动到队首（最近使用）
	c.order.MoveToFront(elem)
	c.hits++
	return entry.value, true
}

// Set 设置缓存值
func (c *LRUCache) Set(key string, value interface{}) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.setWithTTL(key, value, c.ttl)
}

// SetWithTTL 设置缓存值（自定义 TTL）
func (c *LRUCache) SetWithTTL(key string, value interface{}, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.setWithTTL(key, value, ttl)
}

func (c *LRUCache) setWithTTL(key string, value interface{}, ttl time.Duration) {
	// 如果 key 已存在，更新值
	if elem, ok := c.items[key]; ok {
		entry := elem.Value.(*cacheEntry)
		entry.value = value
		entry.expiresAt = time.Now().Add(ttl)
		c.order.MoveToFront(elem)
		return
	}

	// 检查容量
	if c.order.Len() >= c.capacity {
		c.removeOldest()
	}

	// 添加新条目
	entry := &cacheEntry{
		key:       key,
		value:     value,
		expiresAt: time.Now().Add(ttl),
	}
	elem := c.order.PushFront(entry)
	c.items[key] = elem
}

// Delete 删除缓存值
func (c *LRUCache) Delete(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if elem, ok := c.items[key]; ok {
		c.removeElement(elem)
	}
}

// Has 检查键是否存在
func (c *LRUCache) Has(key string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	elem, ok := c.items[key]
	if !ok {
		return false
	}

	entry := elem.Value.(*cacheEntry)
	return !time.Now().After(entry.expiresAt)
}

// Len 返回当前缓存大小
func (c *LRUCache) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.order.Len()
}

// Clear 清空缓存
func (c *LRUCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.items = make(map[string]*list.Element)
	c.order.Init()
}

// Stats 返回缓存统计信息
func (c *LRUCache) Stats() Stats {
	c.mu.RLock()
	defer c.mu.RUnlock()

	var hitRate float64
	total := c.hits + c.misses
	if total > 0 {
		hitRate = float64(c.hits) / float64(total)
	}

	return Stats{
		Size:     c.order.Len(),
		Capacity: c.capacity,
		Hits:     c.hits,
		Misses:   c.misses,
		HitRate:  hitRate,
	}
}

// Stats 缓存统计信息
type Stats struct {
	Size     int
	Capacity int
	Hits     int64
	Misses   int64
	HitRate  float64
}

func (c *LRUCache) removeOldest() {
	elem := c.order.Back()
	if elem != nil {
		c.removeElement(elem)
	}
}

func (c *LRUCache) removeElement(elem *list.Element) {
	c.order.Remove(elem)
	entry := elem.Value.(*cacheEntry)
	delete(c.items, entry.key)
}

func (c *LRUCache) cleanupLoop() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		c.cleanup()
	}
}

func (c *LRUCache) cleanup() {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	for elem := c.order.Back(); elem != nil; {
		entry := elem.Value.(*cacheEntry)
		if now.After(entry.expiresAt) {
			prev := elem.Prev()
			c.removeElement(elem)
			elem = prev
		} else {
			elem = elem.Prev()
		}
	}
}

// GetOrSet 获取或设置缓存值
func (c *LRUCache) GetOrSet(key string, fn func() (interface{}, error)) (interface{}, error) {
	// 先尝试获取
	if val, ok := c.Get(key); ok {
		return val, nil
	}

	// 不存在则调用函数获取
	val, err := fn()
	if err != nil {
		return nil, err
	}

	c.Set(key, val)
	return val, nil
}

// Keys 返回所有缓存键
func (c *LRUCache) Keys() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	keys := make([]string, 0, c.order.Len())
	for key := range c.items {
		keys = append(keys, key)
	}
	return keys
}
