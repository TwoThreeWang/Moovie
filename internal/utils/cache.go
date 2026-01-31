package utils

import (
	"sync"
	"time"

	lru "github.com/hashicorp/golang-lru/v2"
)

// cacheEntry LRU缓存条目，包含值和过期时间
type cacheEntry struct {
	value     interface{}
	expiredAt time.Time
}

// lruCacheInstance 全局LRU缓存实例
type lruCacheInstance struct {
	cache *lru.Cache[string, cacheEntry]
	mu    sync.RWMutex
}

// Cache 全局缓存实例（保持原有变量名）
var Cache *lruCacheInstance

// InitCache 初始化缓存
func InitCache() {
	// 创建LRU缓存，设置最大容量为2000条
	lruCache, _ := lru.New[string, cacheEntry](2000)
	Cache = &lruCacheInstance{
		cache: lruCache,
	}

	// 启动后台清理goroutine
	go Cache.cleanupExpired()
}

// cleanupExpired 后台清理过期数据
func (c *lruCacheInstance) cleanupExpired() {
	ticker := time.NewTicker(5 * time.Minute) // 每5分钟清理一次
	defer ticker.Stop()

	for range ticker.C {
		c.mu.Lock()
		keys := c.cache.Keys()
		now := time.Now()

		for _, key := range keys {
			if entry, ok := c.cache.Get(key); ok {
				if now.After(entry.expiredAt) {
					c.cache.Remove(key)
				}
			}
		}
		c.mu.Unlock()
	}
}

// CacheGet 获取缓存值（保持原有接口）
func CacheGet(key string) (interface{}, bool) {
	return Cache.Get(key)
}

// Get 获取缓存值（LRU缓存实例方法）
func (c *lruCacheInstance) Get(key string) (interface{}, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	entry, ok := c.cache.Get(key)
	if !ok {
		return nil, false
	}

	// 检查是否过期
	if time.Now().After(entry.expiredAt) {
		c.cache.Remove(key)
		return nil, false
	}

	return entry.value, true
}

// CacheSet 设置缓存值（保持原有接口）
func CacheSet(key string, value interface{}, duration time.Duration) {
	Cache.Set(key, value, duration)
}

// Set 设置缓存值（LRU缓存实例方法）
func (c *lruCacheInstance) Set(key string, value interface{}, duration time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	entry := cacheEntry{
		value:     value,
		expiredAt: time.Now().Add(duration),
	}
	c.cache.Add(key, entry)
}

// CacheDelete 删除缓存（保持原有接口）
func CacheDelete(key string) {
	Cache.Delete(key)
}

// Delete 删除缓存（LRU缓存实例方法）
func (c *lruCacheInstance) Delete(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cache.Remove(key)
}

// CacheClear 清空所有缓存（保持原有接口）
func CacheClear() {
	Cache.Clear()
}

// Clear 清空所有缓存（LRU缓存实例方法）
func (c *lruCacheInstance) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cache.Purge()
}

// CacheItem 包装实际的数据，增加过期时间
type CacheItem[T any] struct {
	Value     T
	ExpiredAt time.Time
}

// SearchCache 搜索结果缓存封装
type SearchCache[T any] struct {
	storage *lru.Cache[string, CacheItem[T]]
	ttl     time.Duration
}

// NewSearchCache 初始化，size 是最大缓存条数（如 1000），ttl 是数据有效期（如 1小时）
func NewSearchCache[T any](size int, ttl time.Duration) *SearchCache[T] {
	// lru.New 是线程安全的
	c, _ := lru.New[string, CacheItem[T]](size)
	return &SearchCache[T]{
		storage: c,
		ttl:     ttl,
	}
}

// 1. 【增/改】 Add (LRU 中 Add 会自动处理 Update)
func (c *SearchCache[T]) Set(key string, value T) {
	item := CacheItem[T]{
		Value:     value,
		ExpiredAt: time.Now().Add(c.ttl),
	}
	c.storage.Add(key, item)
}

// 2. 【查】 Get (带过期检查)
func (c *SearchCache[T]) Get(key string) (T, bool) {
	var zero T // 定义泛型零值
	item, ok := c.storage.Get(key)
	if !ok {
		return zero, false
	}

	// 检查是否过期
	if time.Now().After(item.ExpiredAt) {
		c.storage.Remove(key) // 过期删除
		return zero, false
	}

	return item.Value, true
}

// 3. 【删】 Delete
func (c *SearchCache[T]) Delete(key string) {
	c.storage.Remove(key)
}

// 4. 【清空】 Clear
func (c *SearchCache[T]) Clear() {
	c.storage.Purge()
}

// 5. 【获取当前长度】 Len
func (c *SearchCache[T]) Len() int {
	return c.storage.Len()
}
