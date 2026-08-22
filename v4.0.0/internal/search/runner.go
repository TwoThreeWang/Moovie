package search

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// GoroutineRunner 是有并发上限的后台任务执行器：槽位满了就直接丢弃任务而不是排队，
// 这样请求高峰时后台任务不会把内存和数据库连接吃光。
type GoroutineRunner struct {
	timeout time.Duration
	root    context.Context
	cancel  context.CancelFunc

	mu      sync.Mutex
	stopped bool
	wait    sync.WaitGroup
	slots   chan struct{}
	dropped atomic.Uint64
	lastLog atomic.Int64
}

// NewGoroutineRunner 创建后台执行器，每个任务都有独立超时。
func NewGoroutineRunner(timeout time.Duration, maximumConcurrency ...int) *GoroutineRunner {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	maxConcurrency := 8
	if len(maximumConcurrency) > 0 && maximumConcurrency[0] > 0 {
		maxConcurrency = maximumConcurrency[0]
	}
	root, cancel := context.WithCancel(context.Background())
	return &GoroutineRunner{timeout: timeout, root: root, cancel: cancel, slots: make(chan struct{}, maxConcurrency)}
}

// Run 提交任务，忽略是否被丢弃。
func (runner *GoroutineRunner) Run(task func(context.Context)) {
	_ = runner.TryRun(task)
}

// TryRun 返回任务是否获得有界执行槽位。机会型调用方可以在任务被降载时撤销预登记状态。
func (runner *GoroutineRunner) TryRun(task func(context.Context)) bool {
	runner.mu.Lock()
	if runner.stopped {
		runner.mu.Unlock()
		return false
	}
	select {
	case runner.slots <- struct{}{}:
	default:
		runner.mu.Unlock()
		runner.recordDrop()
		return false
	}
	runner.wait.Add(1)
	runner.mu.Unlock()

	go func() {
		defer runner.wait.Done()
		defer func() { <-runner.slots }()
		defer func() {
			if recovered := recover(); recovered != nil {
				slog.Error("background search refresh panicked", "panic", recovered)
			}
		}()
		ctx, cancel := context.WithTimeout(runner.root, runner.timeout)
		defer cancel()
		task(ctx)
	}()
	return true
}

// recordDrop 累计被丢弃的任务数，日志每秒最多一条。
func (runner *GoroutineRunner) recordDrop() {
	total := runner.dropped.Add(1)
	now := time.Now().Unix()
	previous := runner.lastLog.Load()
	if previous == now || !runner.lastLog.CompareAndSwap(previous, now) {
		return
	}
	slog.Warn("background task shed to protect process", "dropped_total", total, "active", len(runner.slots), "limit", cap(runner.slots))
}

// Active/Dropped 供后台监控页展示。
func (runner *GoroutineRunner) Active() int { return len(runner.slots) }

// Dropped 返回因队列满被丢弃的任务数。
func (runner *GoroutineRunner) Dropped() uint64 { return runner.dropped.Load() }

// Stop 取消所有在跑的任务并等待退出，用于优雅停机。
func (runner *GoroutineRunner) Stop(ctx context.Context) error {
	runner.mu.Lock()
	if !runner.stopped {
		runner.stopped = true
		runner.cancel()
	}
	runner.mu.Unlock()

	done := make(chan struct{})
	go func() {
		runner.wait.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
