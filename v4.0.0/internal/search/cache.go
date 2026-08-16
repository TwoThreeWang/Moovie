package search

import (
	"sync"
	"time"
)

type cacheEntry[T any] struct {
	value     T
	expiresAt time.Time
	usedAt    uint64
}

// Cache 是容量有界、并发安全的 TTL 缓存，并按最近最少使用顺序淘汰，
// 行为与旧版 500 条搜索缓存保持一致。
type Cache[T any] struct {
	mu       sync.Mutex
	entries  map[string]cacheEntry[T]
	capacity int
	ttl      time.Duration
	clock    func() time.Time
	sequence uint64
}

func NewCache[T any](capacity int, ttl time.Duration) *Cache[T] {
	if capacity < 1 {
		capacity = 1
	}
	return &Cache[T]{
		entries:  make(map[string]cacheEntry[T], capacity),
		capacity: capacity,
		ttl:      ttl,
		clock:    time.Now,
	}
}

func (cache *Cache[T]) Get(key string) (T, bool) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	var zero T
	entry, exists := cache.entries[key]
	if !exists {
		return zero, false
	}
	if cache.clock().After(entry.expiresAt) {
		delete(cache.entries, key)
		return zero, false
	}
	cache.sequence++
	entry.usedAt = cache.sequence
	cache.entries[key] = entry
	return entry.value, true
}

func (cache *Cache[T]) Set(key string, value T) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	cache.sequence++
	cache.entries[key] = cacheEntry[T]{value: value, expiresAt: cache.clock().Add(cache.ttl), usedAt: cache.sequence}
	if len(cache.entries) <= cache.capacity {
		return
	}
	var oldestKey string
	oldestSequence := ^uint64(0)
	for candidate, entry := range cache.entries {
		if entry.usedAt < oldestSequence {
			oldestKey = candidate
			oldestSequence = entry.usedAt
		}
	}
	delete(cache.entries, oldestKey)
}

func (cache *Cache[T]) Len() int {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	return len(cache.entries)
}
