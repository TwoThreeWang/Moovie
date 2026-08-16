package workqueue

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/platform/database"
)

const (
	StatusPending   = "pending"
	StatusRunning   = "running"
	StatusCompleted = "completed"
	StatusFailed    = "failed"
)

type Job struct {
	ID             int
	TaskType       string
	SubjectKey     string
	Payload        json.RawMessage
	Reason         string
	RequestedBy    int
	Status         string
	Priority       int
	AttemptCount   int
	MaxAttempts    int
	AvailableAt    time.Time
	LockedBy       string
	LockedUntil    *time.Time
	StartedAt      *time.Time
	FinishedAt     *time.Time
	ProgressTotal  int
	ProgressDone   int
	ProgressFailed int
	ProgressCursor string
	ErrorMessage   string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type Spec struct {
	TaskType    string
	SubjectKey  string
	Payload     any
	Reason      string
	RequestedBy int
	Priority    int
	MaxAttempts int
	AvailableAt time.Time
}

type Store interface {
	Enqueue(ctx context.Context, spec Spec) (int, error)
	Claim(ctx context.Context, lease time.Duration) (*Job, error)
	Complete(ctx context.Context, jobID int) error
	Fail(ctx context.Context, job Job, message string) error
	Recover(ctx context.Context, before time.Time) error
	UpdateProgress(ctx context.Context, jobID, total, done, failed int, cursor string) error
	Get(ctx context.Context, jobID int) (*Job, error)
	Latest(ctx context.Context, taskType, subjectKey string) (*Job, error)
	List(ctx context.Context, taskType, status string, before time.Time, limit int) ([]Job, error)
	Reset(ctx context.Context, jobID int) error
}

type PostgresStore struct{ database database.Executor }

func NewPostgresStore(executor database.Executor) *PostgresStore {
	return &PostgresStore{database: executor}
}

const jobColumns = `id, task_type, subject_key, payload, reason, COALESCE(requested_by, 0), status,
priority, attempt_count, max_attempts, available_at, locked_by, locked_until, started_at, finished_at,
progress_total, progress_done, progress_failed, progress_cursor, error_message, created_at, updated_at`

func (store *PostgresStore) Enqueue(ctx context.Context, spec Spec) (int, error) {
	if spec.TaskType == "" || spec.SubjectKey == "" {
		return 0, fmt.Errorf("task_type and subject_key required")
	}
	payload, err := json.Marshal(spec.Payload)
	if err != nil {
		return 0, fmt.Errorf("encode worker payload: %w", err)
	}
	if spec.Reason == "" {
		spec.Reason = "scheduled"
	}
	if spec.MaxAttempts <= 0 {
		spec.MaxAttempts = 5
	}
	if spec.AvailableAt.IsZero() {
		spec.AvailableAt = time.Now()
	}
	var jobID int
	err = store.database.QueryRow(ctx, `INSERT INTO worker_jobs
(task_type, subject_key, payload, reason, requested_by, priority, max_attempts, available_at)
VALUES ($1,$2,$3::jsonb,$4,$5,$6,$7,$8)
ON CONFLICT (task_type, subject_key) WHERE status IN ('pending', 'running') DO UPDATE SET
payload = EXCLUDED.payload, reason = EXCLUDED.reason,
requested_by = COALESCE(EXCLUDED.requested_by, worker_jobs.requested_by),
priority = GREATEST(worker_jobs.priority, EXCLUDED.priority),
available_at = CASE WHEN worker_jobs.status = 'pending' THEN LEAST(worker_jobs.available_at, EXCLUDED.available_at) ELSE worker_jobs.available_at END,
updated_at = NOW()
RETURNING id`, spec.TaskType, spec.SubjectKey, string(payload), spec.Reason, nullableID(spec.RequestedBy),
		spec.Priority, spec.MaxAttempts, spec.AvailableAt).Scan(&jobID)
	if err != nil {
		return 0, fmt.Errorf("enqueue worker job: %w", err)
	}
	return jobID, nil
}

func (store *PostgresStore) Claim(ctx context.Context, lease time.Duration) (*Job, error) {
	if lease <= 0 {
		lease = 30 * time.Minute
	}
	rows, err := store.database.Query(ctx, `WITH candidate AS (
    SELECT id AS job_id FROM worker_jobs
    WHERE status = 'pending' AND available_at <= NOW()
    ORDER BY priority DESC, available_at, id
    FOR UPDATE SKIP LOCKED LIMIT 1
)
UPDATE worker_jobs job SET status = 'running', attempt_count = attempt_count + 1,
locked_by = pg_backend_pid()::text, locked_until = NOW() + $1 * INTERVAL '1 second',
started_at = NOW(), finished_at = NULL, error_message = '', updated_at = NOW()
FROM candidate WHERE job.id = candidate.job_id
RETURNING `+jobColumns, lease.Seconds())
	if err != nil {
		return nil, fmt.Errorf("claim worker job: %w", err)
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, rows.Err()
	}
	job, err := scanJob(rows)
	if err != nil {
		return nil, fmt.Errorf("scan worker job: %w", err)
	}
	return &job, nil
}

func (store *PostgresStore) Complete(ctx context.Context, jobID int) error {
	_, err := store.database.Exec(ctx, `UPDATE worker_jobs SET status = 'completed', finished_at = NOW(),
locked_by = '', locked_until = NULL, error_message = '', updated_at = NOW()
WHERE id = $1 AND status = 'running'`, jobID)
	return err
}

func (store *PostgresStore) Fail(ctx context.Context, job Job, message string) error {
	_, err := store.database.Exec(ctx, `UPDATE worker_jobs SET
status = CASE WHEN attempt_count >= max_attempts THEN 'failed' ELSE 'pending' END,
available_at = CASE attempt_count WHEN 1 THEN NOW() + INTERVAL '15 minutes'
    WHEN 2 THEN NOW() + INTERVAL '1 hour' WHEN 3 THEN NOW() + INTERVAL '6 hours'
    ELSE NOW() + INTERVAL '24 hours' END,
locked_by = '', locked_until = NULL,
finished_at = CASE WHEN attempt_count >= max_attempts THEN NOW() ELSE NULL END,
error_message = $2, updated_at = NOW()
WHERE id = $1 AND status = 'running'`, job.ID, message)
	return err
}

func (store *PostgresStore) Recover(ctx context.Context, before time.Time) error {
	_, err := store.database.Exec(ctx, `UPDATE worker_jobs SET status = 'pending', locked_by = '', locked_until = NULL,
available_at = NOW(), started_at = NULL, finished_at = NULL,
error_message = 'recovered expired lease', updated_at = NOW()
WHERE status = 'running' AND (locked_until IS NULL OR locked_until < $1)`, before)
	return err
}

func (store *PostgresStore) UpdateProgress(ctx context.Context, jobID, total, done, failed int, cursor string) error {
	_, err := store.database.Exec(ctx, `UPDATE worker_jobs SET progress_total=$2, progress_done=$3,
progress_failed=$4, progress_cursor=$5, updated_at=NOW() WHERE id=$1`, jobID, total, done, failed, cursor)
	return err
}

func (store *PostgresStore) Get(ctx context.Context, jobID int) (*Job, error) {
	rows, err := store.database.Query(ctx, `SELECT `+jobColumns+` FROM worker_jobs WHERE id=$1`, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, rows.Err()
	}
	job, err := scanJob(rows)
	return &job, err
}

func (store *PostgresStore) Latest(ctx context.Context, taskType, subjectKey string) (*Job, error) {
	rows, err := store.database.Query(ctx, `SELECT `+jobColumns+` FROM worker_jobs
WHERE task_type=$1 AND subject_key=$2 ORDER BY id DESC LIMIT 1`, taskType, subjectKey)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, rows.Err()
	}
	job, err := scanJob(rows)
	return &job, err
}

func (store *PostgresStore) List(ctx context.Context, taskType, status string, before time.Time, limit int) ([]Job, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := store.database.Query(ctx, `SELECT `+jobColumns+` FROM worker_jobs
WHERE task_type=$1 AND ($2='' OR status=$2) AND ($3::timestamptz IS NULL OR updated_at < $3)
ORDER BY id ASC LIMIT $4`, taskType, status, nullableTime(before), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	jobs := make([]Job, 0, limit)
	for rows.Next() {
		job, scanErr := scanJob(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

func (store *PostgresStore) Reset(ctx context.Context, jobID int) error {
	_, err := store.database.Exec(ctx, `UPDATE worker_jobs SET status='pending', available_at=NOW(),
locked_by='', locked_until=NULL, started_at=NULL, finished_at=NULL, error_message='', updated_at=NOW()
WHERE id=$1 AND status='failed'`, jobID)
	return err
}

func scanJob(row interface{ Scan(...any) error }) (Job, error) {
	var job Job
	err := row.Scan(&job.ID, &job.TaskType, &job.SubjectKey, &job.Payload, &job.Reason, &job.RequestedBy,
		&job.Status, &job.Priority, &job.AttemptCount, &job.MaxAttempts, &job.AvailableAt, &job.LockedBy,
		&job.LockedUntil, &job.StartedAt, &job.FinishedAt, &job.ProgressTotal, &job.ProgressDone,
		&job.ProgressFailed, &job.ProgressCursor, &job.ErrorMessage, &job.CreatedAt, &job.UpdatedAt)
	return job, err
}

func nullableID(value int) any {
	if value <= 0 {
		return nil
	}
	return value
}

func nullableTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value
}

type MemoryStore struct {
	mu     sync.Mutex
	nextID int
	jobs   []Job
}

func NewMemoryStore() *MemoryStore { return &MemoryStore{nextID: 1} }

func (store *MemoryStore) Enqueue(_ context.Context, spec Spec) (int, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	for index := range store.jobs {
		job := &store.jobs[index]
		if job.TaskType == spec.TaskType && job.SubjectKey == spec.SubjectKey && (job.Status == StatusPending || job.Status == StatusRunning) {
			return job.ID, nil
		}
	}
	payload, _ := json.Marshal(spec.Payload)
	now := time.Now()
	job := Job{ID: store.nextID, TaskType: spec.TaskType, SubjectKey: spec.SubjectKey, Payload: payload,
		Reason: spec.Reason, RequestedBy: spec.RequestedBy, Status: StatusPending, Priority: spec.Priority,
		MaxAttempts: spec.MaxAttempts, AvailableAt: spec.AvailableAt, CreatedAt: now, UpdatedAt: now}
	if job.MaxAttempts <= 0 {
		job.MaxAttempts = 5
	}
	if job.AvailableAt.IsZero() {
		job.AvailableAt = now
	}
	store.nextID++
	store.jobs = append(store.jobs, job)
	return job.ID, nil
}

func (store *MemoryStore) Claim(_ context.Context, lease time.Duration) (*Job, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	now := time.Now()
	for index := range store.jobs {
		job := &store.jobs[index]
		if job.Status != StatusPending || job.AvailableAt.After(now) {
			continue
		}
		until := now.Add(lease)
		job.Status, job.AttemptCount, job.StartedAt, job.LockedUntil, job.UpdatedAt = StatusRunning, job.AttemptCount+1, &now, &until, now
		copy := *job
		return &copy, nil
	}
	return nil, nil
}

func (store *MemoryStore) Complete(_ context.Context, jobID int) error {
	return store.finish(jobID, StatusCompleted, "")
}
func (store *MemoryStore) Fail(_ context.Context, job Job, message string) error {
	status := StatusPending
	if job.AttemptCount >= job.MaxAttempts {
		status = StatusFailed
	}
	return store.finish(job.ID, status, message)
}
func (store *MemoryStore) Recover(_ context.Context, before time.Time) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	now := time.Now()
	for index := range store.jobs {
		job := &store.jobs[index]
		if job.Status == StatusRunning && (job.LockedUntil == nil || job.LockedUntil.Before(before)) {
			job.Status, job.LockedUntil, job.LockedBy = StatusPending, nil, ""
			job.StartedAt, job.FinishedAt = nil, nil
			job.AvailableAt, job.UpdatedAt, job.ErrorMessage = now, now, "recovered expired lease"
		}
	}
	return nil
}
func (store *MemoryStore) UpdateProgress(_ context.Context, jobID, total, done, failed int, cursor string) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	for index := range store.jobs {
		if store.jobs[index].ID == jobID {
			store.jobs[index].ProgressTotal, store.jobs[index].ProgressDone, store.jobs[index].ProgressFailed, store.jobs[index].ProgressCursor = total, done, failed, cursor
			return nil
		}
	}
	return nil
}
func (store *MemoryStore) Get(_ context.Context, jobID int) (*Job, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	for index := range store.jobs {
		if store.jobs[index].ID == jobID {
			copy := store.jobs[index]
			return &copy, nil
		}
	}
	return nil, nil
}
func (store *MemoryStore) Latest(_ context.Context, taskType, subjectKey string) (*Job, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	for index := len(store.jobs) - 1; index >= 0; index-- {
		if store.jobs[index].TaskType == taskType && store.jobs[index].SubjectKey == subjectKey {
			copy := store.jobs[index]
			return &copy, nil
		}
	}
	return nil, nil
}
func (store *MemoryStore) List(_ context.Context, taskType, status string, before time.Time, limit int) ([]Job, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	jobs := make([]Job, 0)
	for _, job := range store.jobs {
		if job.TaskType == taskType && (status == "" || job.Status == status) && (before.IsZero() || job.UpdatedAt.Before(before)) {
			jobs = append(jobs, job)
			if limit > 0 && len(jobs) >= limit {
				break
			}
		}
	}
	return jobs, nil
}
func (store *MemoryStore) Reset(_ context.Context, jobID int) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	for index := range store.jobs {
		if store.jobs[index].ID == jobID && store.jobs[index].Status == StatusFailed {
			store.jobs[index].Status = StatusPending
			store.jobs[index].AvailableAt = time.Now()
			store.jobs[index].ErrorMessage = ""
		}
	}
	return nil
}
func (store *MemoryStore) finish(jobID int, status, message string) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	now := time.Now()
	for index := range store.jobs {
		if store.jobs[index].ID == jobID {
			store.jobs[index].Status, store.jobs[index].ErrorMessage, store.jobs[index].UpdatedAt = status, message, now
			if status != StatusPending {
				store.jobs[index].FinishedAt = &now
			}
			return nil
		}
	}
	return nil
}
