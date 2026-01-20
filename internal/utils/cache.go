package utils

import (
	"time"

	lru "github.com/hashicorp/golang-lru/v2"
	"github.com/patrickmn/go-cache"
)

// Cache 全局缓存实例
var Cache *cache.Cache

// InitCache 初始化缓存
func InitCache() {
	// 默认过期时间5分钟，清理间隔10分钟
	Cache = cache.New(5*time.Minute, 10*time.Minute)
}

// CacheGet 获取缓存值
func CacheGet(key string) (interface{}, bool) {
	return Cache.Get(key)
}

// CacheSet 设置缓存值
func CacheSet(key string, value interface{}, duration time.Duration) {
	Cache.Set(key, value, duration)
}

// CacheDelete 删除缓存
func CacheDelete(key string) {
	Cache.Delete(key)
}

// CacheClear 清空所有缓存
func CacheClear() {
	Cache.Flush()
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
