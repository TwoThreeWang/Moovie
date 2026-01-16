DROP INDEX IF EXISTS idx_vod_items_name_trgm;
DROP INDEX IF EXISTS idx_vod_items_sub_trgm;
DROP INDEX IF EXISTS idx_vod_items_en_trgm;
-- 不删除扩展，因为可能其他地方也在用
