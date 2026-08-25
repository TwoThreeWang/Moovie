package workqueue

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/platform/database/testdb"
)

func TestDispatcherHonorsConfiguredConcurrency(t *testing.T) {
	store := NewPostgresStore(testdb.Pool(t))
	for index := 0; index < 6; index++ {
		_, _ = store.Enqueue(t.Context(), Spec{TaskType: "test", SubjectKey: fmt.Sprint(index)})
	}
	var running, maximum, completed atomic.Int32
	release := make(chan struct{})
	dispatcher := NewDispatcher(store, 3, time.Millisecond)
	dispatcher.Handle("test", time.Second, func(context.Context, Job) error {
		current := running.Add(1)
		for {
			previous := maximum.Load()
			if current <= previous || maximum.CompareAndSwap(previous, current) {
				break
			}
		}
		<-release
		running.Add(-1)
		completed.Add(1)
		return nil
	})
	if err := dispatcher.Start(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for maximum.Load() < 3 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if maximum.Load() != 3 {
		t.Fatalf("maximum concurrency = %d, want 3", maximum.Load())
	}
	close(release)
	for completed.Load() < 6 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	stopCtx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := dispatcher.Stop(stopCtx); err != nil {
		t.Fatal(err)
	}
	if completed.Load() != 6 {
		t.Fatalf("completed = %d", completed.Load())
	}
}

func TestPostgresStoreRecoversExpiredLease(t *testing.T) {
	store := NewPostgresStore(testdb.Pool(t))
	_, _ = store.Enqueue(t.Context(), Spec{TaskType: "test", SubjectKey: "expired"})
	claimed, err := store.Claim(t.Context(), time.Minute)
	if err != nil || claimed == nil {
		t.Fatalf("claim = %+v/%v", claimed, err)
	}
	// 直接把租约拨到过去，而不是靠 1ms 租约 + sleep 赌时序：
	// Go 的 time.Now() 与 Postgres 的 NOW() 是两个时钟，毫秒级比较不可靠。
	if _, err := store.database.Exec(t.Context(),
		`UPDATE worker_jobs SET locked_until = NOW() - INTERVAL '1 minute' WHERE id = $1`, claimed.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.Recover(t.Context(), time.Now()); err != nil {
		t.Fatal(err)
	}
	reclaimed, err := store.Claim(t.Context(), time.Second)
	if err != nil || reclaimed == nil || reclaimed.ID != claimed.ID || reclaimed.AttemptCount != 2 {
		t.Fatalf("reclaim = %+v/%v", reclaimed, err)
	}
}
