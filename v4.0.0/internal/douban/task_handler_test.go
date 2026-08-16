package douban

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/identity"
	"github.com/TwoThreeWang/Moovie/new/internal/workqueue"
)

func TestTaskHandlerRunsThroughUnifiedDispatcher(t *testing.T) {
	queue := workqueue.NewMemoryStore()
	jobs := NewMemoryJobStore(queue)
	users := identity.NewMemoryStore()
	user, _ := users.Create(t.Context(), identity.User{Email: "person@example.com", Username: "person", PasswordHash: "hash"})
	_ = users.UpdateDoubanUserID(t.Context(), user.ID, "198878447")
	executor := &recordingExecutor{called: make(chan struct{}, 1)}
	handler := NewTaskHandler(jobs, users, executor)
	if _, err := handler.CreateFull(t.Context(), user.ID); err != nil {
		t.Fatal(err)
	}
	dispatcher := workqueue.NewDispatcher(queue, 2, time.Millisecond)
	dispatcher.Handle(TaskSync, time.Second, handler.Handle)
	if err := dispatcher.Start(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-executor.called:
	case <-time.After(time.Second):
		t.Fatal("sync task was not executed")
	}
	stopCtx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := dispatcher.Stop(stopCtx); err != nil {
		t.Fatal(err)
	}
	if executor.fullCalls.Load() != 1 {
		t.Fatalf("full calls = %d", executor.fullCalls.Load())
	}
}

func TestDailyTaskGeneratesMonthlyReportOnFirstDay(t *testing.T) {
	queue := workqueue.NewMemoryStore()
	handler := NewTaskHandler(NewMemoryJobStore(queue), identity.NewMemoryStore(), &recordingExecutor{}, WithMonthlyGenerator(&recordingMonthlyGenerator{}))
	generator := &recordingMonthlyGenerator{}
	handler.monthly = generator
	handler.now = func() time.Time { return time.Date(2026, time.August, 1, 3, 0, 0, 0, time.Local) }
	if err := handler.HandleDaily(t.Context(), workqueue.Job{}); err != nil {
		t.Fatal(err)
	}
	if generator.calls.Load() != 1 {
		t.Fatalf("monthly calls = %d", generator.calls.Load())
	}
}

type recordingExecutor struct {
	fullCalls atomic.Int32
	called    chan struct{}
}

func (executor *recordingExecutor) SyncFull(context.Context, int, string, int) error {
	executor.fullCalls.Add(1)
	if executor.called != nil {
		executor.called <- struct{}{}
	}
	return nil
}
func (*recordingExecutor) SyncIncremental(context.Context, int, string, int) error { return nil }

type recordingMonthlyGenerator struct{ calls atomic.Int32 }

func (generator *recordingMonthlyGenerator) GeneratePreviousMonth(context.Context) error {
	generator.calls.Add(1)
	return nil
}
