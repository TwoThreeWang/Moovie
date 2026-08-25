// Package ratelimit 提供按客户端 IP 的固定窗口限流。
// 弹幕上游请求和播放事件上报原本各有一份一模一样的实现，这里合成一份。
package ratelimit

import (
	"sync"
	"time"
)

// window 是单个 IP 在当前时间窗口内的计数。
type window struct {
	count int
	reset time.Time
}

// PerIP 按客户端 IP 做固定窗口限流。用在对外开放、无需登录的接口上，
// 不限流会被刷。
type PerIP struct {
	mu       sync.Mutex
	counts   map[string]window
	max      int
	capacity int
	window   time.Duration
	now      func() time.Time
}

// NewPerIP 创建限流器：每个 IP 每个窗口最多 max 次，最多记 8192 个 IP。
func NewPerIP(max int, per time.Duration) *PerIP {
	return &PerIP{counts: make(map[string]window), max: max, capacity: 8192, window: per, now: time.Now}
}

// Allow 判断是否放行。表满时先清理过期条目，仍然满就直接拒绝，防止内存无限增长。
func (limiter *PerIP) Allow(clientIP string) bool {
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	now := limiter.now()
	entry, exists := limiter.counts[clientIP]
	if !exists || !now.Before(entry.reset) {
		if !exists && len(limiter.counts) >= limiter.capacity {
			for key, candidate := range limiter.counts {
				if !now.Before(candidate.reset) {
					delete(limiter.counts, key)
				}
			}
			if len(limiter.counts) >= limiter.capacity {
				return false
			}
		}
		limiter.counts[clientIP] = window{count: 1, reset: now.Add(limiter.window)}
		return true
	}
	if entry.count >= limiter.max {
		return false
	}
	entry.count++
	limiter.counts[clientIP] = entry
	return true
}
