package workqueue

import (
	"context"
	"encoding/json"
	"fmt"
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
	ThrottleCount  int
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
	Fail(ctx context.Context, job Job, failure Failure) error
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
priority, attempt_count, max_attempts, throttle_count, available_at, locked_by, locked_until, started_at, finished_at,
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

// Fail 按失败类型收尾。三条分支的差别是刻意的：
//   - 终止错误直接判死，不浪费四次退避去重试一个不存在的条目；
//   - 限流退还本次 attempt（Claim 时已 +1），改用秒级退避，另由 throttle_count 兜底；
//   - 其余错误维持原来的 15 分钟 / 1 小时 / 6 小时 / 24 小时阶梯。
//
// SQL 里 CASE 右侧读到的都是本次 UPDATE 之前的旧值，所以 attempt_count 仍是 Claim 后的计数，
// 而 throttle_count 需要手动 +1 才是本次的次数。
func (store *PostgresStore) Fail(ctx context.Context, job Job, failure Failure) error {
	throttled := failure.Outcome == OutcomeThrottled
	terminal := failure.Outcome == OutcomeTerminal
	_, err := store.database.Exec(ctx, `UPDATE worker_jobs SET
attempt_count = CASE WHEN $3 THEN GREATEST(attempt_count - 1, 0) ELSE attempt_count END,
throttle_count = CASE WHEN $3 THEN throttle_count + 1 ELSE throttle_count END,
status = CASE WHEN $4 THEN 'failed'
    WHEN $3 THEN CASE WHEN throttle_count + 1 >= $6::int THEN 'failed' ELSE 'pending' END
    WHEN attempt_count >= max_attempts THEN 'failed' ELSE 'pending' END,
available_at = CASE WHEN $5::double precision > 0 THEN NOW() + INTERVAL '1 second' * $5::double precision
    WHEN $3 THEN NOW() + INTERVAL '30 seconds'
    WHEN attempt_count = 1 THEN NOW() + INTERVAL '15 minutes'
    WHEN attempt_count = 2 THEN NOW() + INTERVAL '1 hour'
    WHEN attempt_count = 3 THEN NOW() + INTERVAL '6 hours'
    ELSE NOW() + INTERVAL '24 hours' END,
locked_by = '', locked_until = NULL,
finished_at = CASE WHEN $4 OR ($3 AND throttle_count + 1 >= $6::int)
    OR (NOT $3 AND attempt_count >= max_attempts) THEN NOW() ELSE NULL END,
error_message = $2, updated_at = NOW()
WHERE id = $1 AND status = 'running'`,
		job.ID, failure.Message, throttled, terminal, failure.RetryAfter.Seconds(), maxThrottleAttempts)
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

// retryAssignments 归还完整的重试预算。人工重试的意图是「重新跑一遍」，
// 保留 attempt_count 会让任务被领取后失败一次就立刻回到 failed，按钮等于只按了半下。
const retryAssignments = `status = 'pending', available_at = NOW(), attempt_count = 0, throttle_count = 0,
locked_by = '', locked_until = NULL, started_at = NULL, finished_at = NULL,
error_message = '', updated_at = NOW()`

func (store *PostgresStore) Reset(ctx context.Context, jobID int) error {
	_, err := store.RetryJob(ctx, jobID)
	return err
}

// RetryJob 把单个失败任务放回队列，返回实际恢复的行数。
// 同一 (task_type, subject_key) 已经有 pending/running 任务时不做任何事：
// 活跃索引是唯一的，硬改会直接撞约束，而且那份工作本来就已经排上了。
func (store *PostgresStore) RetryJob(ctx context.Context, jobID int) (int, error) {
	affected, err := store.database.Exec(ctx, `UPDATE worker_jobs job SET `+retryAssignments+`
WHERE job.id = $1 AND job.status = 'failed' AND NOT EXISTS (
    SELECT 1 FROM worker_jobs active
    WHERE active.task_type = job.task_type AND active.subject_key = job.subject_key
      AND active.status IN ('pending', 'running'))`, jobID)
	if err != nil {
		return 0, fmt.Errorf("retry worker job: %w", err)
	}
	return int(affected), nil
}

// RetryFailed 批量恢复失败任务。taskType 为空表示不限类型；limit 是一次恢复的上限，
// 免得一次点击就把几千个请求同时甩给刚刚才恢复的上游。
// DISTINCT ON 是必需的：failed 状态不受活跃唯一索引约束，同一对象可能堆了多行失败记录，
// 一起转成 pending 会撞唯一索引，这里只恢复最新的那条。
func (store *PostgresStore) RetryFailed(ctx context.Context, taskType string, limit int) (int, error) {
	if limit <= 0 || limit > 2000 {
		limit = 500
	}
	affected, err := store.database.Exec(ctx, `WITH candidate AS (
    SELECT DISTINCT ON (job.task_type, job.subject_key) job.id
    FROM worker_jobs job
    WHERE job.status = 'failed' AND ($1 = '' OR job.task_type = $1)
      AND NOT EXISTS (
        SELECT 1 FROM worker_jobs active
        WHERE active.task_type = job.task_type AND active.subject_key = job.subject_key
          AND active.status IN ('pending', 'running'))
    ORDER BY job.task_type, job.subject_key, job.id DESC
    LIMIT $2
)
UPDATE worker_jobs SET `+retryAssignments+`
FROM candidate WHERE worker_jobs.id = candidate.id`, taskType, limit)
	if err != nil {
		return 0, fmt.Errorf("retry failed worker jobs: %w", err)
	}
	return int(affected), nil
}

func scanJob(row interface{ Scan(...any) error }) (Job, error) {
	var job Job
	err := row.Scan(&job.ID, &job.TaskType, &job.SubjectKey, &job.Payload, &job.Reason, &job.RequestedBy,
		&job.Status, &job.Priority, &job.AttemptCount, &job.MaxAttempts, &job.ThrottleCount, &job.AvailableAt, &job.LockedBy,
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
