-- 资源冷却批次系统未实际生效（cold 状态不影响搜索），移除相关表和列。

ALTER TABLE vod_items DROP COLUMN IF EXISTS cold_at;
ALTER TABLE vod_items DROP COLUMN IF EXISTS lifecycle_batch_id;

DROP TABLE IF EXISTS resource_lifecycle_batch_items;
DROP TABLE IF EXISTS resource_lifecycle_batches;

DROP INDEX IF EXISTS vod_items_cold_idx;
