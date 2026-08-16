package douban

import "time"

type SyncStatus string

const (
	StatusPending   SyncStatus = "pending"
	StatusRunning   SyncStatus = "running"
	StatusCompleted SyncStatus = "completed"
	StatusFailed    SyncStatus = "failed"
)

type SyncType string

const (
	TypeFull        SyncType = "full"
	TypeIncremental SyncType = "incremental"
)

type Job struct {
	ID           int
	UserID       int
	Status       SyncStatus
	SyncType     SyncType
	AttemptCount int
	Total        int
	Processed    int
	FailedCount  int
	Cursor       string
	ErrorMessage string
	StartedAt    *time.Time
	FinishedAt   *time.Time
	CreatedAt    time.Time
	UpdatedAt    time.Time
}
