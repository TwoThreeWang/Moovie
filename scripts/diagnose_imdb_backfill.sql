-- IMDb 映射回填诊断脚本
-- 用法：psql "$DSN" -f scripts/diagnose_imdb_backfill.sql
-- 对应日志：INFO IMDb 映射回填 candidates=200 resolved=0 settled=0

\echo '=== 1. 待补总量与 imdb_lookup_at 分布 ==='
-- 如果 never_looked 远大于 200 且 looked 长期为 0，说明队列在原地打转。
SELECT
    COUNT(*)                                                        AS pending_total,
    COUNT(*) FILTER (WHERE m.imdb_lookup_at IS NULL)                AS never_looked,
    COUNT(*) FILTER (WHERE m.imdb_lookup_at IS NOT NULL)            AS looked_before,
    MIN(m.imdb_lookup_at)                                           AS oldest_lookup,
    MAX(m.imdb_lookup_at)                                           AS newest_lookup
FROM media m
WHERE m.douban_id <> ''
  AND NOT EXISTS (SELECT 1 FROM media_external_ids x
                  WHERE x.media_id = m.id AND x.provider = 'imdb');

\echo ''
\echo '=== 2. 队首 200 条的豆瓣 ID 合规性（validDoubanID：6-9 位纯数字）==='
-- bad_format > 0 就意味着 wikidataQuery 会静默丢掉它们，
-- 全部不合规时连 HTTP 请求都不会发出，日志却和「查了但没命中」长得一模一样。
WITH head AS (
    SELECT m.id, m.douban_id, m.media_type, m.title, m.year
    FROM media m
    WHERE m.douban_id <> ''
      AND (m.imdb_lookup_at IS NULL OR m.imdb_lookup_at < NOW() - INTERVAL '30 days')
      AND NOT EXISTS (SELECT 1 FROM media_external_ids x
                      WHERE x.media_id = m.id AND x.provider = 'imdb')
    ORDER BY m.imdb_lookup_at NULLS FIRST, m.id
    LIMIT 200
)
SELECT
    COUNT(*)                                                                    AS head_count,
    COUNT(*) FILTER (WHERE douban_id ~ '^[0-9]{6,9}$')                           AS valid_format,
    COUNT(*) FILTER (WHERE douban_id !~ '^[0-9]{6,9}$')                          AS bad_format,
    COUNT(*) FILTER (WHERE douban_id ~ '^[0-9]+$' AND length(douban_id) > 9)     AS too_long,
    COUNT(*) FILTER (WHERE douban_id ~ '^[0-9]+$' AND length(douban_id) < 6)     AS too_short,
    COUNT(*) FILTER (WHERE douban_id !~ '^[0-9]+$')                              AS non_numeric
FROM head;

\echo ''
\echo '=== 3. 队首 20 条样本（拿去手工核对 Wikidata / wmdb）==='
SELECT m.id, m.douban_id, m.media_type, left(m.title, 30) AS title, m.year, m.imdb_lookup_at
FROM media m
WHERE m.douban_id <> ''
  AND (m.imdb_lookup_at IS NULL OR m.imdb_lookup_at < NOW() - INTERVAL '30 days')
  AND NOT EXISTS (SELECT 1 FROM media_external_ids x
                  WHERE x.media_id = m.id AND x.provider = 'imdb')
ORDER BY m.imdb_lookup_at NULLS FIRST, m.id
LIMIT 20;

\echo ''
\echo '=== 4. 队首按 media_type 分布 ==='
-- 剧集在 Wikidata 上的 P4529 覆盖率远低于电影；如果队首全是 tv，resolved=0 就不奇怪。
WITH head AS (
    SELECT m.media_type
    FROM media m
    WHERE m.douban_id <> ''
      AND (m.imdb_lookup_at IS NULL OR m.imdb_lookup_at < NOW() - INTERVAL '30 days')
      AND NOT EXISTS (SELECT 1 FROM media_external_ids x
                      WHERE x.media_id = m.id AND x.provider = 'imdb')
    ORDER BY m.imdb_lookup_at NULLS FIRST, m.id
    LIMIT 200
)
SELECT media_type, COUNT(*) FROM head GROUP BY media_type ORDER BY 2 DESC;

\echo ''
\echo '=== 5. 回填任务近期是否真的在推进 ==='
-- 每分钟调度一次。如果 24 小时内 imdb_lookup_at 被更新的行数接近 0，
-- 就印证了「每轮 settled=0，时间戳永远不写」。
SELECT
    COUNT(*) FILTER (WHERE imdb_lookup_at > NOW() - INTERVAL '1 hour')  AS marked_last_hour,
    COUNT(*) FILTER (WHERE imdb_lookup_at > NOW() - INTERVAL '24 hours') AS marked_last_24h,
    COUNT(*) FILTER (WHERE imdb_lookup_at > NOW() - INTERVAL '7 days')  AS marked_last_7d
FROM media
WHERE douban_id <> '';

\echo ''
\echo '=== 6. 已有多少 IMDb 映射（分子/分母）==='
SELECT
    (SELECT COUNT(*) FROM media WHERE douban_id <> '')                          AS media_with_douban,
    (SELECT COUNT(DISTINCT media_id) FROM media_external_ids WHERE provider = 'imdb') AS media_with_imdb;

\echo ''
\echo '=== 7. worker_jobs 里 imdb_backfill 的执行状况 ==='
SELECT status, COUNT(*), MAX(finished_at) AS last_finished,
       MAX(attempt_count) AS max_attempts, MAX(left(error_message, 120)) AS sample_error
FROM worker_jobs
WHERE task_type = 'imdb_backfill'
GROUP BY status ORDER BY 2 DESC;

\echo ''
\echo '=== 8. 同一个 IMDb ID 被多个 media 争抢（会导致映射来回搬家）==='
-- SaveIMDbID 的 ON CONFLICT (provider, external_type, external_id) DO UPDATE media_id
-- 会把映射从旧 media 抢到新 media，被抢走的那条又变回「没有 imdb」。
SELECT provider, external_type, external_id, COUNT(*) AS rows_seen
FROM media_external_ids
WHERE provider = 'imdb'
GROUP BY 1, 2, 3
HAVING COUNT(*) > 1
ORDER BY 4 DESC
LIMIT 20;
