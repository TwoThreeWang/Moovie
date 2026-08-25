-- 由服务端管理观影历史同步游标。旧 watch_histories 表继续作为读取模型，
-- 此只追加账本为 V2 客户端提供幂等、单调游标和删除标记。

CREATE TABLE IF NOT EXISTS history_sync_events (
    version BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    device_id TEXT NOT NULL,
    operation_id TEXT NOT NULL,
    device_seq BIGINT NOT NULL DEFAULT 0,
    operation_type TEXT NOT NULL CHECK (operation_type IN ('upsert', 'delete', 'complete')),
    record_id BIGINT,
    media_id BIGINT REFERENCES media(id) ON DELETE SET NULL,
    season_number INTEGER NOT NULL DEFAULT 1,
    episode_key TEXT NOT NULL DEFAULT '',
    source_key TEXT NOT NULL DEFAULT '',
    vod_id TEXT NOT NULL DEFAULT '',
    payload_json JSONB NOT NULL DEFAULT '{}'::jsonb,
    conflict_reason TEXT NOT NULL DEFAULT '',
    occurred_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (user_id, device_id, operation_id)
);

CREATE INDEX IF NOT EXISTS history_sync_events_user_version_idx
    ON history_sync_events (user_id, version);
