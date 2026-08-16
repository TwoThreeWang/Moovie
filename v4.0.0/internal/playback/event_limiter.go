package playback

import (
	"sync"
	"time"
)

type playbackEventLimit struct {
	count int
	reset time.Time
}

type playbackEventLimiter struct {
	mu       sync.Mutex
	counts   map[string]playbackEventLimit
	max      int
	capacity int
	window   time.Duration
	now      func() time.Time
}

func newPlaybackEventLimiter(max int, window time.Duration) *playbackEventLimiter {
	return &playbackEventLimiter{counts: make(map[string]playbackEventLimit), max: max, capacity: 8192, window: window, now: time.Now}
}

func (limiter *playbackEventLimiter) Allow(clientIP string) bool {
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
		limiter.counts[clientIP] = playbackEventLimit{count: 1, reset: now.Add(limiter.window)}
		return true
	}
	if entry.count >= limiter.max {
		return false
	}
	entry.count++
	limiter.counts[clientIP] = entry
	return true
}
