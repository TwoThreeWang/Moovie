-- 只增加常量默认值列，不在结构迁移中全表回填或创建大型索引。
SET LOCAL lock_timeout = '3s';
ALTER TABLE vod_items ADD COLUMN playback_cleaned BOOLEAN NOT NULL DEFAULT FALSE;
-- 新资源已经通过共享清洗器；旧资源保留 FALSE 作为可恢复的工作标记。
ALTER TABLE vod_items ALTER COLUMN playback_cleaned SET DEFAULT TRUE;
ALTER TABLE media ADD COLUMN playback_reconciled_at TIMESTAMPTZ;
ALTER TABLE media ADD COLUMN playback_history_repaired BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE media ALTER COLUMN playback_history_repaired SET DEFAULT TRUE;
