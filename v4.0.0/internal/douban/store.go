package douban

import (
	"context"
	"time"
)

// JobStore 是同步任务的读写接口。
type JobStore interface {
	Create(ctx context.Context, userID int, syncType SyncType) (*Job, error)
	LatestByUser(ctx context.Context, userID int) (*Job, error)
	UpdateTotal(ctx context.Context, jobID, total int) error
	UpdateProgress(ctx context.Context, jobID, processed, failed int, cursor string) error
	HasActive(ctx context.Context, userID int) (bool, error)
	RetryableBefore(ctx context.Context, before time.Time, limit int) ([]Job, error)
	ResetPending(ctx context.Context, jobID int) error
}
