package utils

import (
	"time"

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
