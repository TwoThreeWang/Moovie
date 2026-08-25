-- 0031_finalize_canonical_cutover.sql：数据导入复验通过后，删除最终系统不再读写的过渡结构。
-- 新系统只保留 media、playback_positions 和新资源线路模型。

DO $$
BEGIN
    -- 有业务数据的升级库必须先完整执行 cmd/datamigrate。全新空库没有待转换数据，
    -- 因此可以直接完成建表；这样既保证首次安装简单，也阻止 Web 自动迁移误删旧数据。
    IF (
        EXISTS (SELECT 1 FROM movies)
        OR EXISTS (SELECT 1 FROM watch_histories)
        OR EXISTS (SELECT 1 FROM resource_episodes)
        OR EXISTS (SELECT 1 FROM user_movies)
    ) AND NOT EXISTS (
        SELECT 1 FROM schema_migrations WHERE version = 'data-canonical-cutover-ready'
    ) THEN
        RAISE EXCEPTION 'canonical data migration has not completed; run scripts/migrate.sh before 0031';
    END IF;

    -- 即使完成标记存在，片单仍必须全部关联规范媒体；否则删表后无法修复原始身份。
    IF EXISTS (SELECT 1 FROM user_movies WHERE media_id IS NULL) THEN
        RAISE EXCEPTION 'user_movies still contains rows without media_id; refusing canonical finalization';
    END IF;
END $$;

UPDATE media_aliases SET source='douban', updated_at=NOW() WHERE source='legacy';
ALTER TABLE media_aliases ALTER COLUMN source SET DEFAULT 'manual';

ALTER TABLE playback_positions DROP COLUMN IF EXISTS legacy_history_id;
ALTER TABLE playback_positions DROP COLUMN IF EXISTS legacy_payload;

DROP TABLE IF EXISTS legacy_media_mappings;
DROP TABLE IF EXISTS resource_playback_health;
DROP TABLE IF EXISTS resource_episodes;
DROP TABLE IF EXISTS watch_histories;
DROP TABLE IF EXISTS movies;

ALTER TABLE vod_items DROP COLUMN IF EXISTS avg_speed_ms;
ALTER TABLE vod_items DROP COLUMN IF EXISTS sample_count;
ALTER TABLE vod_items DROP COLUMN IF EXISTS failed_count;
