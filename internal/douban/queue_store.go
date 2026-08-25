package douban

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/workqueue"
)

// TaskSync 是豆瓣同步在任务队列中的类型名。
const TaskSync = "douban_sync"

// QueueJobStore 把豆瓣同步任务直接存进通用任务队列，不另建表。
type QueueJobStore struct{ queue workqueue.Store }

// NewQueueJobStore 创建任务存储。
func NewQueueJobStore(store workqueue.Store) *QueueJobStore { return &QueueJobStore{queue: store} }

// Create 创建一个同步任务。
func (store *QueueJobStore) Create(ctx context.Context, userID int, syncType SyncType) (*Job, error) {
	if syncType == "" {
		syncType = TypeFull
	}
	id, err := store.queue.Enqueue(ctx, workqueue.Spec{TaskType: TaskSync, SubjectKey: strconv.Itoa(userID),
		Payload: map[string]any{"user_id": userID, "sync_type": syncType}, Reason: "account_sync"})
	if err != nil {
		return nil, err
	}
	job, err := store.queue.Latest(ctx, TaskSync, strconv.Itoa(userID))
	if err != nil {
		return nil, err
	}
	if job == nil {
		return &Job{ID: id, UserID: userID, SyncType: syncType, Status: StatusPending}, nil
	}
	return mapJob(*job)
}

// LatestByUser 查用户最近一次同步任务，用于设置页显示进度。
func (store *QueueJobStore) LatestByUser(ctx context.Context, userID int) (*Job, error) {
	job, err := store.queue.Latest(ctx, TaskSync, strconv.Itoa(userID))
	if err != nil || job == nil {
		return nil, err
	}
	return mapJob(*job)
}

// UpdateTotal 写入任务总条数。
func (store *QueueJobStore) UpdateTotal(ctx context.Context, jobID, total int) error {
	return store.queue.UpdateProgress(ctx, jobID, total, 0, 0, "")
}

// UpdateProgress 更新同步进度。
func (store *QueueJobStore) UpdateProgress(ctx context.Context, jobID, processed, failed int, cursor string) error {
	job, err := store.queue.Get(ctx, jobID)
	if err != nil {
		return err
	}
	total := 0
	if job != nil {
		total = job.ProgressTotal
	}
	return store.queue.UpdateProgress(ctx, jobID, total, processed, failed, cursor)
}

// HasActive 判断用户是否已有同步在跑，避免重复触发。
func (store *QueueJobStore) HasActive(ctx context.Context, userID int) (bool, error) {
	job, err := store.queue.Latest(ctx, TaskSync, strconv.Itoa(userID))
	return job != nil && (job.Status == workqueue.StatusPending || job.Status == workqueue.StatusRunning), err
}

// RetryableBefore 找出卡住可重试的任务。
func (store *QueueJobStore) RetryableBefore(ctx context.Context, before time.Time, limit int) ([]Job, error) {
	rows, err := store.queue.List(ctx, TaskSync, workqueue.StatusFailed, before, limit)
	if err != nil {
		return nil, err
	}
	jobs := make([]Job, 0, len(rows))
	for _, row := range rows {
		job, mapErr := mapJob(row)
		if mapErr != nil {
			return nil, mapErr
		}
		jobs = append(jobs, *job)
	}
	return jobs, nil
}

// ResetPending 把任务重置为待执行。
func (store *QueueJobStore) ResetPending(ctx context.Context, jobID int) error {
	return store.queue.Reset(ctx, jobID)
}

// mapJob 把队列任务映射成豆瓣同步任务视图。
func mapJob(source workqueue.Job) (*Job, error) {
	userID, err := strconv.Atoi(source.SubjectKey)
	if err != nil {
		return nil, fmt.Errorf("invalid Douban sync subject %q", source.SubjectKey)
	}
	payload := struct {
		SyncType SyncType `json:"sync_type"`
	}{}
	_ = json.Unmarshal(source.Payload, &payload)
	if payload.SyncType == "" {
		payload.SyncType = TypeFull
	}
	return &Job{ID: source.ID, UserID: userID, Status: SyncStatus(source.Status), SyncType: payload.SyncType,
		AttemptCount: source.AttemptCount, Total: source.ProgressTotal, Processed: source.ProgressDone,
		FailedCount: source.ProgressFailed, Cursor: source.ProgressCursor, ErrorMessage: source.ErrorMessage,
		StartedAt: source.StartedAt, FinishedAt: source.FinishedAt, CreatedAt: source.CreatedAt, UpdatedAt: source.UpdatedAt}, nil
}
