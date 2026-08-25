// Package douban 负责把用户的豆瓣「想看 / 看过」同步进本站片单。
//
// 同步任务本身不单独建表，直接复用 worker_jobs（见 queue_store.go），
// 数据最终写入 user_movies。
//
// 两种同步方式：
//
//	全量：翻豆瓣接口的所有分页，第一次绑定时用。
//	增量：先读 RSS 拿到最近有变动的条目，再只翻到覆盖这些条目为止，每天定时跑。
package douban

import "time"

// SyncStatus 是同步任务状态，与 workqueue 的状态一一对应。
type SyncStatus string

// 同步任务的状态。
const (
	StatusPending   SyncStatus = "pending"
	StatusRunning   SyncStatus = "running"
	StatusCompleted SyncStatus = "completed"
	StatusFailed    SyncStatus = "failed"
)

// SyncType 是同步方式。
type SyncType string

// 同步方式：全量翻完所有分页，增量只翻到覆盖 RSS 里的变动条目为止。
const (
	TypeFull        SyncType = "full"
	TypeIncremental SyncType = "incremental"
)

// Job 是一次同步任务的展示视图，由 worker_jobs 的记录映射而来。
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
