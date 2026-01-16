-- 开启 pg_trgm 扩展用于模糊搜索优化
CREATE EXTENSION IF NOT EXISTS pg_trgm;

-- 为 vod_items 表添加 GIN 索引以加速 LIKE '%keyword%' 查询
CREATE INDEX IF NOT EXISTS idx_vod_items_name_trgm ON vod_items USING GIN (vod_name gin_trgm_ops);
CREATE INDEX IF NOT EXISTS idx_vod_items_sub_trgm ON vod_items USING GIN (vod_sub gin_trgm_ops);
CREATE INDEX IF NOT EXISTS idx_vod_items_en_trgm ON vod_items USING GIN (vod_en gin_trgm_ops);
