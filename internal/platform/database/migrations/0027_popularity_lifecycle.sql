-- 建立可审计热门快照和可恢复资源冷却。
-- 本 migration 只增加结构，不删除任何旧数据行或元数据。

CREATE TABLE IF NOT EXISTS popularity_snapshot_runs (
    id BIGSERIAL PRIMARY KEY,
    media_type TEXT NOT NULL CHECK (media_type IN ('movie', 'tv', 'show', 'cartoon')),
    status TEXT NOT NULL DEFAULT 'building' CHECK (status IN ('building', 'ready', 'failed')),
    source_status JSONB NOT NULL DEFAULT '{}'::jsonb,
    item_count INTEGER NOT NULL DEFAULT 0,
    generated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ NOT NULL,
    completed_at TIMESTAMPTZ,
    error_message TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS popularity_snapshot_runs_ready_idx
    ON popularity_snapshot_runs (media_type, generated_at DESC)
    WHERE status = 'ready';

CREATE TABLE IF NOT EXISTS popularity_snapshots (
    run_id BIGINT NOT NULL REFERENCES popularity_snapshot_runs(id) ON DELETE CASCADE,
    media_id BIGINT NOT NULL REFERENCES media(id) ON DELETE CASCADE,
    rank INTEGER NOT NULL CHECK (rank > 0),
    rrf_score DOUBLE PRECISION NOT NULL DEFAULT 0,
    final_score DOUBLE PRECISION NOT NULL DEFAULT 0,
    source_ranks JSONB NOT NULL DEFAULT '{}'::jsonb,
    subject_payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    playable_candidate_count INTEGER NOT NULL DEFAULT 0,
    quality_multiplier DOUBLE PRECISION NOT NULL DEFAULT 1,
    freshness_boost DOUBLE PRECISION NOT NULL DEFAULT 0,
    generated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (run_id, media_id),
    UNIQUE (run_id, rank)
);
CREATE INDEX IF NOT EXISTS popularity_snapshots_run_rank_idx
    ON popularity_snapshots (run_id, rank);
CREATE INDEX IF NOT EXISTS popularity_snapshots_media_idx
    ON popularity_snapshots (media_id, generated_at DESC);

CREATE TABLE IF NOT EXISTS resource_lifecycle_batches (
    id BIGSERIAL PRIMARY KEY,
    action TEXT NOT NULL CHECK (action IN ('cool')),
    status TEXT NOT NULL DEFAULT 'previewed' CHECK (status IN ('previewed', 'applied', 'expired')),
    cutoff_at TIMESTAMPTZ NOT NULL,
    candidate_count INTEGER NOT NULL DEFAULT 0,
    source_distribution JSONB NOT NULL DEFAULT '{}'::jsonb,
    status_distribution JSONB NOT NULL DEFAULT '{}'::jsonb,
    history_reference_count INTEGER NOT NULL DEFAULT 0,
    unique_resource_count INTEGER NOT NULL DEFAULT 0,
    sample_records JSONB NOT NULL DEFAULT '[]'::jsonb,
    estimated_bytes BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ NOT NULL,
    applied_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS resource_lifecycle_batches_status_idx
    ON resource_lifecycle_batches (status, expires_at);

CREATE TABLE IF NOT EXISTS resource_lifecycle_batch_items (
    batch_id BIGINT NOT NULL REFERENCES resource_lifecycle_batches(id) ON DELETE CASCADE,
    source_key TEXT NOT NULL,
    vod_id TEXT NOT NULL,
    previous_status TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (batch_id, source_key, vod_id)
);

ALTER TABLE vod_items ADD COLUMN IF NOT EXISTS cold_at TIMESTAMPTZ;
ALTER TABLE vod_items ADD COLUMN IF NOT EXISTS last_discovered_at TIMESTAMPTZ;
ALTER TABLE vod_items ADD COLUMN IF NOT EXISTS last_success_at TIMESTAMPTZ;
ALTER TABLE vod_items ADD COLUMN IF NOT EXISTS lifecycle_batch_id BIGINT REFERENCES resource_lifecycle_batches(id);
CREATE INDEX IF NOT EXISTS vod_items_cold_idx
    ON vod_items (resource_status, cold_at DESC);
