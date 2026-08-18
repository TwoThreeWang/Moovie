package douban

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/workqueue"
)

const TaskSync = "douban_sync"

type QueueJobStore struct{ queue workqueue.Store }


func NewQueueJobStore(store workqueue.Store) *QueueJobStore { return &QueueJobStore{queue: store} }

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

func (store *QueueJobStore) LatestByUser(ctx context.Context, userID int) (*Job, error) {
	job, err := store.queue.Latest(ctx, TaskSync, strconv.Itoa(userID))
	if err != nil || job == nil {
		return nil, err
	}
	return mapJob(*job)
}

func (store *QueueJobStore) UpdateTotal(ctx context.Context, jobID, total int) error {
	return store.queue.UpdateProgress(ctx, jobID, total, 0, 0, "")
}

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

func (store *QueueJobStore) HasActive(ctx context.Context, userID int) (bool, error) {
	job, err := store.queue.Latest(ctx, TaskSync, strconv.Itoa(userID))
	return job != nil && (job.Status == workqueue.StatusPending || job.Status == workqueue.StatusRunning), err
}

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

func (store *QueueJobStore) ResetPending(ctx context.Context, jobID int) error {
	return store.queue.Reset(ctx, jobID)
}

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
