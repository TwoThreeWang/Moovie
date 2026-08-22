-- media_source_snapshots.next_refresh_at 是第二条刷新阶梯（3/7/14/30/90 天），
-- 每次抓取都算一遍、还带一个索引，但全代码库没有任何地方读它——真正在调度的是
-- media.next_refresh_at（internal/catalog/refresh.go 读的那条）。
-- 两条阶梯并存只会让人以为刷新节奏是这条定的。
--
-- 顺序要求：先部署去掉写入的新代码，再跑这条迁移。旧进程仍会写这一列，
-- 列没了它的 INSERT 会报错。

DROP INDEX IF EXISTS media_source_snapshots_refresh_idx;
ALTER TABLE media_source_snapshots DROP COLUMN IF EXISTS next_refresh_at;
