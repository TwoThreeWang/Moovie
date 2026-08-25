// Package cache 提供一个容量有界的 TTL + LRU 内存缓存。
// 搜索结果、媒体身份、弹幕和相似推荐原本各有一份一模一样的实现，这里合成一份。
package cache

import (
	"sync"
	"time"
)

// entry 是一条缓存记录，usedAt 是自增序号，用于 LRU 淘汰。
type entry[T any] struct {
	value     T
	expiresAt time.Time
	usedAt    uint64
}

// TTL 是容量有界、并发安全的 TTL 缓存，超出容量时按最近最少使用顺序淘汰。
type TTL[T any] struct {
	mu       sync.Mutex
	entries  map[string]entry[T]
	capacity int
	ttl      time.Duration
	clock    func() time.Time
	sequence uint64
}

// New 创建容量有界的 TTL 缓存。
func New[T any](capacity int, ttl time.Duration) *TTL[T] {
	if capacity < 1 {
		capacity = 1
	}
	return &TTL[T]{
		entries:  make(map[string]entry[T], capacity),
		capacity: capacity,
		ttl:      ttl,
		clock:    time.Now,
	}
}

// Get 读缓存，过期的记录顺手删掉。
func (cache *TTL[T]) Get(key string) (T, bool) {
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

// Set 写缓存，超出容量时淘汰最久未使用的一条。
// ponytail: 淘汰用的是 O(n) 全表扫描，容量只有几百条时够用；上万条再换成链表。
func (cache *TTL[T]) Set(key string, value T) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	cache.sequence++
	cache.entries[key] = entry[T]{value: value, expiresAt: cache.clock().Add(cache.ttl), usedAt: cache.sequence}
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

// GetStale 读缓存，过期的记录仍然返回（stale=true），只有完全不存在才返回 false。
func (cache *TTL[T]) GetStale(key string) (value T, stale bool, ok bool) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	var zero T
	e, exists := cache.entries[key]
	if !exists {
		return zero, false, false
	}
	expired := cache.clock().After(e.expiresAt)
	cache.sequence++
	e.usedAt = cache.sequence
	cache.entries[key] = e
	return e.value, expired, true
}

// Len 返回当前缓存条数。
func (cache *TTL[T]) Len() int {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	return len(cache.entries)
}
