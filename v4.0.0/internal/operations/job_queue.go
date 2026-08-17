package operations

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"
)

type JobQueueReader interface {
	JobQueue(context.Context, JobQueueQuery) (JobQueueSnapshot, error)
}

type JobQueueQuery struct {
	Status    string
	Direction string
	Cursor    int64
	Limit     int
}

type JobCounts struct{ Pending, Running, Completed, Failed int64 }

type WorkerJob struct {
	ID             int64      `json:"id"`
	TaskType       string     `json:"task_type"`
	SubjectKey     string     `json:"subject_key"`
	Reason         string     `json:"reason"`
	RequestedBy    *int64     `json:"requested_by"`
	Status         string     `json:"status"`
	Priority       int        `json:"priority"`
	AttemptCount   int        `json:"attempt_count"`
	MaxAttempts    int        `json:"max_attempts"`
	AvailableAt    time.Time  `json:"available_at"`
	LockedBy       string     `json:"locked_by"`
	LockedUntil    *time.Time `json:"locked_until"`
	StartedAt      *time.Time `json:"started_at"`
	FinishedAt     *time.Time `json:"finished_at"`
	ProgressTotal  int        `json:"progress_total"`
	ProgressDone   int        `json:"progress_done"`
	ProgressFailed int        `json:"progress_failed"`
	ErrorMessage   string     `json:"error_message"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

type JobQueueSnapshot struct {
	Counts JobCounts    `json:"counts"`
	Jobs   []WorkerJob  `json:"jobs"`
	Page   JobQueuePage `json:"-"`
}

type JobQueuePage struct {
	HasPrevious, HasNext       bool
	PreviousCursor, NextCursor int64
}

func (store *MetricsStore) JobQueue(ctx context.Context, query JobQueueQuery) (JobQueueSnapshot, error) {
	if query.Status != "pending" && query.Status != "running" && query.Status != "completed" && query.Status != "failed" {
		query.Status = ""
	}
	if query.Direction != "prev" {
		query.Direction = "next"
	}
	if query.Cursor < 1 {
		query.Cursor = 0
	}
	if query.Limit < 1 || query.Limit > 100 {
		query.Limit = 50
	}
	if store == nil || store.database == nil {
		return JobQueueSnapshot{Jobs: []WorkerJob{}}, nil
	}
	var payload []byte
	if err := store.database.QueryRow(ctx, jobQueueSQL, query.Status, query.Cursor, query.Direction, query.Limit+1).Scan(&payload); err != nil {
		return JobQueueSnapshot{}, fmt.Errorf("query job queue: %w", err)
	}
	var snapshot JobQueueSnapshot
	if err := json.Unmarshal(payload, &snapshot); err != nil {
		return JobQueueSnapshot{}, fmt.Errorf("decode job queue: %w", err)
	}
	if snapshot.Jobs == nil {
		snapshot.Jobs = []WorkerJob{}
	}
	snapshot.paginate(query)
	return snapshot, nil
}

func (snapshot *JobQueueSnapshot) paginate(query JobQueueQuery) {
	hasExtra := len(snapshot.Jobs) > query.Limit
	if hasExtra {
		snapshot.Jobs = snapshot.Jobs[:query.Limit]
	}
	if query.Direction == "prev" {
		slices.Reverse(snapshot.Jobs)
	}
	if len(snapshot.Jobs) == 0 {
		return
	}
	snapshot.Page.HasPrevious, snapshot.Page.HasNext = query.Cursor > 0, hasExtra
	if query.Direction == "prev" {
		snapshot.Page.HasPrevious, snapshot.Page.HasNext = hasExtra, query.Cursor > 0
	}
	snapshot.Page.PreviousCursor = snapshot.Jobs[0].ID
	snapshot.Page.NextCursor = snapshot.Jobs[len(snapshot.Jobs)-1].ID
}

func (store *MetricsStore) DeleteExpiredJobs(ctx context.Context, completedBefore, failedBefore time.Time, limit int) (int, error) {
	if store == nil || store.database == nil {
		return 0, nil
	}
	if limit < 1 || limit > 1000 {
		limit = 1000
	}
	affected, err := store.database.Exec(ctx, `WITH expired AS (
    SELECT id FROM worker_jobs
    WHERE (status='completed' AND updated_at<$1) OR (status='failed' AND updated_at<$2)
    ORDER BY id LIMIT $3
)
DELETE FROM worker_jobs jobs USING expired WHERE jobs.id=expired.id`, completedBefore, failedBefore, limit)
	return int(affected), err
}

const jobQueueSQL = `WITH counts AS (
    SELECT COUNT(*) FILTER (WHERE status='pending') AS pending,
           COUNT(*) FILTER (WHERE status='running') AS running,
           COUNT(*) FILTER (WHERE status='completed') AS completed,
           COUNT(*) FILTER (WHERE status='failed') AS failed
    FROM worker_jobs
), items AS (
    SELECT id, task_type, subject_key, reason, requested_by, status, priority,
           attempt_count, max_attempts, throttle_count, available_at, locked_by, locked_until,
           started_at, finished_at, progress_total, progress_done, progress_failed,
           error_message, created_at, updated_at
    FROM worker_jobs
    WHERE ($1='' OR status=$1)
      AND ($2=0 OR ($3='prev' AND id>$2) OR ($3<>'prev' AND id<$2))
    ORDER BY CASE WHEN $3='prev' THEN id END ASC, CASE WHEN $3<>'prev' THEN id END DESC
    LIMIT $4
)
SELECT JSONB_BUILD_OBJECT(
    'counts', (SELECT TO_JSONB(counts) FROM counts),
    'jobs', COALESCE((SELECT JSONB_AGG(TO_JSONB(item) ORDER BY CASE WHEN $3='prev' THEN id END ASC, CASE WHEN $3<>'prev' THEN id END DESC) FROM items item), '[]'::JSONB)
)`
