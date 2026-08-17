package outbound

import (
	"context"
	"sync"
	"time"
)

// Limiter 给共享的免费上游接口配速。它做两件事：把请求按最小间隔串行化，
// 以及在收到 429 之后让所有调用方一起冷却——单个 worker 自己退避没有意义，
// 限流额度是整个进程共享的。
type Limiter struct {
	mu       sync.Mutex
	interval time.Duration
	next     time.Time
}

func NewLimiter(interval time.Duration) *Limiter {
	if interval < 0 {
		interval = 0
	}
	return &Limiter{interval: interval}
}

// Wait 阻塞到下一个可用发送时刻。名额在返回前就已经占住，所以并发调用会自然排成队列。
func (limiter *Limiter) Wait(ctx context.Context) error {
	if limiter == nil {
		return nil
	}
	limiter.mu.Lock()
	now := time.Now()
	start := limiter.next
	if start.Before(now) {
		start = now
	}
	limiter.next = start.Add(limiter.interval)
	limiter.mu.Unlock()

	delay := time.Until(start)
	if delay <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Pause 在已有节奏之上追加一段冷却，用于响应 429 或 Retry-After。
func (limiter *Limiter) Pause(duration time.Duration) {
	if limiter == nil || duration <= 0 {
		return
	}
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	until := time.Now().Add(duration)
	if until.After(limiter.next) {
		limiter.next = until
	}
}
