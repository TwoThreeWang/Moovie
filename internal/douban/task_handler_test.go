package douban

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/identity"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/database/testdb"
	"github.com/TwoThreeWang/Moovie/new/internal/workqueue"
)

func TestTaskHandlerRunsThroughUnifiedDispatcher(t *testing.T) {
	queue := workqueue.NewPostgresStore(testdb.Pool(t))
	jobs := NewQueueJobStore(queue)
	users := identity.NewPostgresStore(testdb.Pool(t))
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
