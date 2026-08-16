-- 资源生命周期独立于规范媒体和历史。资源行可以变为 stale 或 retired，
-- 但不能因此删除媒体资料或用户历史。

ALTER TABLE vod_items ADD COLUMN IF NOT EXISTS last_seen_at TIMESTAMPTZ;
ALTER TABLE vod_items ADD COLUMN IF NOT EXISTS last_accessed_at TIMESTAMPTZ;
ALTER TABLE vod_items ADD COLUMN IF NOT EXISTS last_played_at TIMESTAMPTZ;
ALTER TABLE vod_items ADD COLUMN IF NOT EXISTS resource_status TEXT NOT NULL DEFAULT 'active';
ALTER TABLE vod_items ADD COLUMN IF NOT EXISTS metadata_hash TEXT NOT NULL DEFAULT '';
ALTER TABLE vod_items ADD COLUMN IF NOT EXISTS metadata_version INTEGER NOT NULL DEFAULT 1;
ALTER TABLE vod_items ADD COLUMN IF NOT EXISTS stale_at TIMESTAMPTZ;
ALTER TABLE vod_items ADD COLUMN IF NOT EXISTS retired_at TIMESTAMPTZ;
ALTER TABLE vod_items ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW();

UPDATE vod_items
SET last_seen_at = COALESCE(last_seen_at, last_visited_at),
    updated_at = COALESCE(updated_at, last_visited_at, NOW())
WHERE last_seen_at IS NULL;

CREATE INDEX IF NOT EXISTS vod_items_lifecycle_idx
    ON vod_items (resource_status, last_seen_at DESC);
