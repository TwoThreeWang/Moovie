package search

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

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

func (runner *GoroutineRunner) recordDrop() {
	total := runner.dropped.Add(1)
	now := time.Now().Unix()
	previous := runner.lastLog.Load()
	if previous == now || !runner.lastLog.CompareAndSwap(previous, now) {
		return
	}
	slog.Warn("background task shed to protect process", "dropped_total", total, "active", len(runner.slots), "limit", cap(runner.slots))
}

func (runner *GoroutineRunner) Active() int     { return len(runner.slots) }
func (runner *GoroutineRunner) Dropped() uint64 { return runner.dropped.Load() }

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
