CREATE TABLE douban_sync_jobs (
    id bigserial PRIMARY KEY,
    user_id bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'running', 'completed', 'failed')),
    sync_type text NOT NULL DEFAULT 'full' CHECK (sync_type IN ('full', 'incremental')),
    total integer NOT NULL DEFAULT 0,
    processed integer NOT NULL DEFAULT 0,
    failed_count integer NOT NULL DEFAULT 0,
    cursor text NOT NULL DEFAULT '',
    error_message text NOT NULL DEFAULT '',
    started_at timestamptz,
    finished_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT NOW(),
    updated_at timestamptz NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_douban_sync_jobs_pending ON douban_sync_jobs (status, id);
CREATE INDEX idx_douban_sync_jobs_user_latest ON douban_sync_jobs (user_id, id DESC);
CREATE INDEX idx_douban_sync_jobs_failed_retry ON douban_sync_jobs (updated_at) WHERE status = 'failed';
CREATE UNIQUE INDEX idx_douban_sync_jobs_user_active ON douban_sync_jobs (user_id)
WHERE status IN ('pending', 'running');
