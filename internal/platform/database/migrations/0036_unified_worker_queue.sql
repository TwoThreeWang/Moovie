-- DATA-WORKER-QUEUE-CUTOVER-READY
-- 一次性把现有两套任务记录迁移到统一队列，随后删除旧表；不保留双写和兼容读取。
CREATE TABLE worker_jobs (
    id BIGSERIAL PRIMARY KEY,
    task_type TEXT NOT NULL,
    subject_key TEXT NOT NULL,
    payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    reason TEXT NOT NULL DEFAULT 'manual',
    requested_by BIGINT REFERENCES users(id) ON DELETE SET NULL,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'running', 'completed', 'failed')),
    priority INTEGER NOT NULL DEFAULT 0,
    attempt_count INTEGER NOT NULL DEFAULT 0,
    max_attempts INTEGER NOT NULL DEFAULT 5,
    available_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    locked_by TEXT NOT NULL DEFAULT '',
    locked_until TIMESTAMPTZ,
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    progress_total INTEGER NOT NULL DEFAULT 0,
    progress_done INTEGER NOT NULL DEFAULT 0,
    progress_failed INTEGER NOT NULL DEFAULT 0,
    progress_cursor TEXT NOT NULL DEFAULT '',
    error_message TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX worker_jobs_active_uidx
    ON worker_jobs (task_type, subject_key) WHERE status IN ('pending', 'running');
CREATE INDEX worker_jobs_pending_idx
    ON worker_jobs (priority DESC, available_at, id) WHERE status = 'pending';
CREATE INDEX worker_jobs_retention_idx
    ON worker_jobs (status, updated_at, id) WHERE status IN ('completed', 'failed');

INSERT INTO worker_jobs
(task_type, subject_key, payload, reason, requested_by, status, attempt_count, max_attempts,
 available_at, started_at, finished_at, error_message, created_at, updated_at)
SELECT CASE provider
        WHEN 'douban' THEN 'douban_metadata'
        WHEN 'douban_reviews' THEN 'douban_reviews'
        WHEN 'tmdb' THEN 'tmdb'
        WHEN 'embedding' THEN 'embedding'
        ELSE provider
    END,
    douban_id, JSONB_BUILD_OBJECT('douban_id', douban_id), reason,
    CASE WHEN EXISTS (SELECT 1 FROM users WHERE users.id = metadata_refresh_jobs.requested_by)
         THEN requested_by ELSE NULL END,
    CASE WHEN status = 'running' THEN 'pending' ELSE status END,
    attempt_count, max_attempts, available_at,
    CASE WHEN status = 'running' THEN NULL ELSE started_at END,
    finished_at, error_message, created_at, updated_at
FROM metadata_refresh_jobs
ON CONFLICT (task_type, subject_key) WHERE status IN ('pending', 'running') DO NOTHING;

INSERT INTO worker_jobs
(task_type, subject_key, payload, reason, status, attempt_count, max_attempts, available_at,
 started_at, finished_at, progress_total, progress_done, progress_failed, progress_cursor,
 error_message, created_at, updated_at)
SELECT 'douban_sync', user_id::text,
    JSONB_BUILD_OBJECT('user_id', user_id, 'sync_type', sync_type),
    'account_sync', CASE WHEN status = 'running' THEN 'pending' ELSE status END,
    attempt_count, 5, created_at,
    CASE WHEN status = 'running' THEN NULL ELSE started_at END,
    finished_at, total, processed, failed_count, cursor,
    error_message, created_at, updated_at
FROM douban_sync_jobs
ON CONFLICT (task_type, subject_key) WHERE status IN ('pending', 'running') DO NOTHING;

DROP TABLE metadata_refresh_jobs;
DROP TABLE douban_sync_jobs;
