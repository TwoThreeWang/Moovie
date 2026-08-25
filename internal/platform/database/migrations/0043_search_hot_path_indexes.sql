-- 补齐三条「每次搜索都要跑、但一直在全表扫」的查询所缺的索引。
-- 纯加索引，不动任何表结构和数据，查询语句也不用改。
--
-- 注意：迁移是在一个事务里跑的，所以不能用 CREATE INDEX CONCURRENTLY。
-- 建索引期间 vod_items 的写入（爬虫回填）会被短暂阻塞，读不受影响。

-- 1) 本地资源搜索：WHERE vod_name LIKE '%kw%' OR vod_sub LIKE '%kw%' OR vod_en LIKE '%kw%'
--    （internal/search/postgres.go 的 Search）。
--    三个列必须都建，少一个 Postgres 就没法对 OR 做 BitmapOr，只能退回全表扫。
--    pg_trgm 扩展在 0019 已经建过了。
CREATE INDEX IF NOT EXISTS vod_items_name_trgm_idx ON vod_items USING GIN (vod_name gin_trgm_ops);
CREATE INDEX IF NOT EXISTS vod_items_sub_trgm_idx ON vod_items USING GIN (vod_sub gin_trgm_ops);
CREATE INDEX IF NOT EXISTS vod_items_en_trgm_idx ON vod_items USING GIN (vod_en gin_trgm_ops);

-- 2) 搜索页豆瓣卡片下方的播放源：WHERE vod_douban_id = $1
--    （internal/search/unified_postgres.go 的 ListResourcesByDoubanID）。
--    这条以前没有任何索引可用，每次搜索都要把整张 vod_items 扫一遍。
--    绝大多数资源没有豆瓣 ID，所以建成部分索引，省一大截体积。
CREATE INDEX IF NOT EXISTS vod_items_douban_idx ON vod_items (vod_douban_id) WHERE vod_douban_id <> '';

-- 3) 资源测速汇总：resourceQualityJoin 那个 LATERAL 对搜索结果的每一行都要按
--    candidate_id 查一次 playback_quality_rollups，而主键是 (bucket, candidate_id)，
--    单查 candidate_id 用不上，只能扫最近 7 天的全部分桶。
CREATE INDEX IF NOT EXISTS playback_quality_rollups_candidate_idx
    ON playback_quality_rollups (candidate_id, bucket DESC);
