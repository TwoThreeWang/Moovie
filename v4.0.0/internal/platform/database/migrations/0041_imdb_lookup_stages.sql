-- 0041_imdb_lookup_stages.sql：把批量源和兜底源的尝试记录分开。
-- 0040 只加了一个 imdb_lookup_at，但回填其实有两个成本相差三个数量级的阶段：
-- Wikidata 一次 SPARQL 问 200 条几乎免费，wmdb 每条之间要间隔 1.2 秒。
-- 共用一个时间戳的后果是：Wikidata 明确回答「这些我这儿都没有」的条目，
-- 因为兜底源当轮没轮到它们而永远不打时间戳，于是永远排在队首被反复重查，
-- 后面新入库的条目一辈子排不上——线上表现就是 candidates=200 resolved=0 settled=0 无限循环。
-- 拆成两列之后两个队列各自推进：批量阶段快速扫全库，兜底阶段只啃批量源确认查不到的那部分。
ALTER TABLE media ADD COLUMN imdb_batch_lookup_at TIMESTAMPTZ;

-- 已经被 0040 记过一次的条目视为两个阶段都走过，避免上线瞬间把全库重新扫一遍。
UPDATE media SET imdb_batch_lookup_at = imdb_lookup_at WHERE imdb_lookup_at IS NOT NULL;

-- 豆瓣 ID 的格式校验从 Go 侧下推到索引条件。6-9 位纯数字之外的值在 wikidataQuery 里
-- 会被静默丢弃（连 HTTP 请求都不会发出），留在候选里只会让日志上的
-- 「查了没命中」和「根本没查」无法区分。索引条件与 validDoubanID 必须保持一致。
DROP INDEX IF EXISTS media_imdb_lookup_idx;

CREATE INDEX media_imdb_batch_lookup_idx ON media (imdb_batch_lookup_at NULLS FIRST, id)
    WHERE douban_id ~ '^[0-9]{6,9}$';

-- 兜底队列只包含批量源已经给过结论的条目：没被批量源看过就轮不到最贵的这个源。
CREATE INDEX media_imdb_fallback_lookup_idx ON media (imdb_lookup_at NULLS FIRST, id)
    WHERE douban_id ~ '^[0-9]{6,9}$' AND imdb_batch_lookup_at IS NOT NULL;
