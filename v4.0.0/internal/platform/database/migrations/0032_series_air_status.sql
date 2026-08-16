-- 追剧更新时间：为剧集类媒体记录连载状态，并加速按播出日期的查询。
-- 本 migration 只增加结构，不删除任何旧数据行或元数据。

-- series_status 保存 TMDB 的连载状态原值，例如：
-- 'Returning Series' / 'Ended' / 'Canceled' / 'In Production' / 'Planned'。
-- 留空表示未知（尚未同步过 TMDB 详情，或该媒体是电影）。
-- 判断"是否还会更新"由应用层归一化，这里不做 CHECK 约束，
-- 避免 TMDB 新增状态值时写入直接失败。
ALTER TABLE media ADD COLUMN IF NOT EXISTS series_status TEXT NOT NULL DEFAULT '';

-- 追剧日历和详情页"下一集何时更新"都是
-- "给定 media，找 air_date 在某个范围内的集"这一种访问形态。
-- media_units_media_order_idx 按 season/episode 排序，无法覆盖按日期的范围扫描。
CREATE INDEX IF NOT EXISTS media_units_air_date_idx
    ON media_units (media_id, air_date)
    WHERE air_date IS NOT NULL;

-- 首页"今日更新"是先按日期收敛、再回连用户在看列表，
-- 因此还需要一个以 air_date 打头的入口，避免按 media_id 前缀索引退化成全表扫描。
CREATE INDEX IF NOT EXISTS media_units_air_date_only_idx
    ON media_units (air_date, media_id)
    WHERE air_date IS NOT NULL AND unit_type = 'episode';
