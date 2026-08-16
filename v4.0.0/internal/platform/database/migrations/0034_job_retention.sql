-- 支持 Worker 任务按终态和更新时间小批量清理；pending/running 不进入索引。
CREATE INDEX IF NOT EXISTS metadata_refresh_jobs_retention_idx
    ON metadata_refresh_jobs (status, updated_at, id)
    WHERE status IN ('completed', 'failed');

CREATE INDEX IF NOT EXISTS douban_sync_jobs_retention_idx
    ON douban_sync_jobs (status, updated_at, id)
    WHERE status IN ('completed', 'failed');
