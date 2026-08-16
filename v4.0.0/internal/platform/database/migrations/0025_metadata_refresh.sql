-- 保存确定性的元数据刷新状态和可持久化的强制刷新任务。
-- 所有变更均为附加结构，分阶段迁移期间旧 movies 表和当前读取路径继续可用。

ALTER TABLE media ADD COLUMN IF NOT EXISTS content_hash TEXT NOT NULL DEFAULT '';
ALTER TABLE media ADD COLUMN IF NOT EXISTS semantic_hash TEXT NOT NULL DEFAULT '';
ALTER TABLE media ADD COLUMN IF NOT EXISTS completeness_score INTEGER NOT NULL DEFAULT 0;
ALTER TABLE media ADD COLUMN IF NOT EXISTS merge_rule_version INTEGER NOT NULL DEFAULT 1;
ALTER TABLE media ADD COLUMN IF NOT EXISTS unchanged_refresh_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE media ADD COLUMN IF NOT EXISTS next_refresh_at TIMESTAMPTZ;
ALTER TABLE media ADD COLUMN IF NOT EXISTS last_content_change_at TIMESTAMPTZ;
CREATE INDEX IF NOT EXISTS media_next_refresh_idx
    ON media (next_refresh_at) WHERE next_refresh_at IS NOT NULL;

ALTER TABLE media_field_sources ADD COLUMN IF NOT EXISTS merge_rule_version INTEGER NOT NULL DEFAULT 1;

ALTER TABLE media_source_snapshots ADD COLUMN IF NOT EXISTS unchanged_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE media_source_snapshots ADD COLUMN IF NOT EXISTS next_refresh_at TIMESTAMPTZ;
ALTER TABLE media_source_snapshots ADD COLUMN IF NOT EXISTS changed_at TIMESTAMPTZ;
CREATE INDEX IF NOT EXISTS media_source_snapshots_refresh_idx
    ON media_source_snapshots (next_refresh_at) WHERE next_refresh_at IS NOT NULL;

ALTER TABLE movies ADD COLUMN IF NOT EXISTS embedding_semantic_hash TEXT NOT NULL DEFAULT '';

CREATE TABLE IF NOT EXISTS metadata_refresh_jobs (
    id BIGSERIAL PRIMARY KEY,
    douban_id TEXT NOT NULL,
    provider TEXT NOT NULL DEFAULT 'douban',
    reason TEXT NOT NULL DEFAULT 'manual',
    requested_by BIGINT,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'running', 'completed', 'failed')),
    attempt_count INTEGER NOT NULL DEFAULT 0,
    max_attempts INTEGER NOT NULL DEFAULT 5,
    available_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    locked_by TEXT NOT NULL DEFAULT '',
    locked_until TIMESTAMPTZ,
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    error_message TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX IF NOT EXISTS metadata_refresh_jobs_active_uidx
    ON metadata_refresh_jobs (provider, douban_id)
    WHERE status IN ('pending', 'running');
CREATE INDEX IF NOT EXISTS metadata_refresh_jobs_pending_idx
    ON metadata_refresh_jobs (available_at, id)
    WHERE status = 'pending';
