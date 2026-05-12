package utils

import (
	"context"
	"log"
	"sync"
	"time"
)

var (
	// asyncWg 用于追踪所有受 GoSafe 保护的异步协程
	asyncWg sync.WaitGroup
)

// GoSafe 在一个受保护的协程中执行任务
// 1. 提供 panic 捕获和恢复
// 2. 将任务计入全局 WaitGroup 供优雅停机时等待
// 3. 如果 timeout > 0，则注入带超时的 Context 给任务函数
func GoSafe(timeout time.Duration, fn func(ctx context.Context)) {
	asyncWg.Add(1)
	go func() {
		defer asyncWg.Done()
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[Async] 异步协程发生 Panic 并恢复: %v", r)
			}
		}()

		var ctx context.Context
		var cancel context.CancelFunc

		if timeout > 0 {
			ctx, cancel = context.WithTimeout(context.Background(), timeout)
			defer cancel()
		} else {
			ctx = context.Background()
		}

		fn(ctx)
	}()
}

// WaitAsync 等待所有后台协程完成，最多等待 timeout 时间
// 适用于程序优雅退出时
func WaitAsync(timeout time.Duration) {
	done := make(chan struct{})
	go func() {
		asyncWg.Wait()
		close(done)
	}()

	select {
	case <-done:
		log.Println("[Async] 所有后台协程已安全退出")
	case <-time.After(timeout):
		log.Printf("[Async] 等待后台协程退出超时 (%v)，将强制退出", timeout)
	}
}
