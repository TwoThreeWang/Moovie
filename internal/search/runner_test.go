package search

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestGoroutineRunnerCancelsAndWaitsDuringStop(t *testing.T) {
	runner := NewGoroutineRunner(time.Minute)
	started := make(chan struct{})
	finished := make(chan struct{})
	runner.Run(func(ctx context.Context) {
		close(started)
		<-ctx.Done()
		close(finished)
	})
	<-started
	stopContext, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := runner.Stop(stopContext); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	select {
	case <-finished:
	default:
		t.Fatal("task did not observe shutdown cancellation")
	}
}

func TestGoroutineRunnerShedsWorkAboveConcurrencyLimit(t *testing.T) {
	runner := NewGoroutineRunner(time.Minute, 2)
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	for index := 0; index < 2; index++ {
		runner.Run(func(context.Context) {
			started <- struct{}{}
			<-release
		})
	}
	<-started
	<-started
	var unexpected atomic.Bool
	runner.Run(func(context.Context) { unexpected.Store(true) })
	if runner.Active() != 2 || runner.Dropped() != 1 || unexpected.Load() {
		t.Fatalf("runner state = active:%d dropped:%d unexpected:%v", runner.Active(), runner.Dropped(), unexpected.Load())
	}
	close(release)
	stopContext, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := runner.Stop(stopContext); err != nil {
		t.Fatal(err)
	}
}
