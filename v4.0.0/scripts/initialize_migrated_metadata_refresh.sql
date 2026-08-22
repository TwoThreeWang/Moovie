-- 初始化迁移媒体的完整度与刷新时间，避免详情页把所有旧媒体同时排进 Worker 队列。
--
-- 默认只预览并回滚：
--   psql "$DSN" -f scripts/initialize_migrated_metadata_refresh.sql
--
-- 确认预览数量后显式提交：
--   psql "$DSN" -v apply=1 -f scripts/initialize_migrated_metadata_refresh.sql
--
-- 只处理从未经过新刷新流水线的迁移行；不会删除或重置 worker_jobs。

\set ON_ERROR_STOP on
\if :{?apply}
\else
\set apply 0
\endif

BEGIN;

CREATE TEMP TABLE migrated_metadata_refresh_plan ON COMMIT DROP AS
WITH scored AS (
    SELECT
        media.id,
        media.douban_id,
        CASE
            WHEN BTRIM(media.title) <> ''
             AND (BTRIM(media.summary) <> '' OR BTRIM(media.original_title) <> '')
                THEN 'ready'
            ELSE 'partial'
        END AS metadata_status,
        (CASE WHEN LOWER(BTRIM(media.title))          NOT IN ('', '[]', '{}', 'null') THEN 15 ELSE 0 END
       + CASE WHEN LOWER(BTRIM(media.year))           NOT IN ('', '[]', '{}', 'null') THEN  8 ELSE 0 END
       + CASE WHEN LOWER(BTRIM(media.media_type))     NOT IN ('', '[]', '{}', 'null') THEN  7 ELSE 0 END
       + CASE WHEN LOWER(BTRIM(media.poster))         NOT IN ('', '[]', '{}', 'null') THEN 10 ELSE 0 END
       + CASE WHEN LOWER(BTRIM(media.summary))        NOT IN ('', '[]', '{}', 'null') THEN 10 ELSE 0 END
       + CASE WHEN LOWER(BTRIM(media.genres))         NOT IN ('', '[]', '{}', 'null') THEN  8 ELSE 0 END
       + CASE WHEN LOWER(BTRIM(media.countries))      NOT IN ('', '[]', '{}', 'null') THEN  5 ELSE 0 END
       + CASE WHEN LOWER(BTRIM(media.directors))      NOT IN ('', '[]', '{}', 'null') THEN  8 ELSE 0 END
       + CASE WHEN LOWER(BTRIM(media.actors))         NOT IN ('', '[]', '{}', 'null') THEN  8 ELSE 0 END
       + CASE WHEN LOWER(BTRIM(media.duration))       NOT IN ('', '[]', '{}', 'null') THEN  5 ELSE 0 END
       + CASE WHEN (SELECT COUNT(*) FROM media_external_ids external_id
                    WHERE external_id.media_id = media.id) >= 2 THEN 8 ELSE 0 END
       + CASE WHEN EXISTS (SELECT 1 FROM media_units unit
                           WHERE unit.media_id = media.id) THEN 8 ELSE 0 END) AS completeness_score
    FROM media
    WHERE media.douban_id <> ''
      AND media.completeness_score = 0
      AND media.next_refresh_at IS NULL
      AND media.last_metadata_sync_at IS NULL
      AND media.content_hash = ''
      AND media.semantic_hash = ''
)
SELECT
    scored.id,
    scored.douban_id,
    scored.metadata_status,
    scored.completeness_score,
    CASE
        -- 不完整资料分散到未来 1-30 天；已有足够展示字段的资料放到 30-90 天。
        -- 用 media.id 生成确定性偏移，使同一批数据稳定地均匀散开。
        WHEN scored.completeness_score < 70 THEN
            NOW() + INTERVAL '1 day'
            + MOD(scored.id * 7919, 30 * 24 * 60 * 60) * INTERVAL '1 second'
        ELSE
            NOW() + INTERVAL '30 days'
            + MOD(scored.id * 7919, 60 * 24 * 60 * 60) * INTERVAL '1 second'
    END AS next_refresh_at
FROM scored;

\echo '=== 计划摘要（尚未修改） ==='
SELECT
    COUNT(*) AS target_rows,
    COUNT(*) FILTER (WHERE metadata_status = 'ready') AS ready_rows,
    COUNT(*) FILTER (WHERE metadata_status = 'partial') AS partial_rows,
    COUNT(*) FILTER (WHERE completeness_score < 70) AS below_70_rows,
    COUNT(*) FILTER (WHERE completeness_score >= 70) AS at_least_70_rows,
    MIN(next_refresh_at) AS earliest_refresh,
    MAX(next_refresh_at) AS latest_refresh
FROM migrated_metadata_refresh_plan;

\echo ''
\echo '=== 完整度分布（尚未修改） ==='
SELECT
    CASE
        WHEN completeness_score < 40 THEN '00-39'
        WHEN completeness_score < 70 THEN '40-69'
        ELSE '70-100'
    END AS score_band,
    COUNT(*) AS rows
FROM migrated_metadata_refresh_plan
GROUP BY 1
ORDER BY 1;

\echo ''
\echo '=== 每日计划量（尚未修改） ==='
SELECT next_refresh_at::date AS refresh_date, COUNT(*) AS rows
FROM migrated_metadata_refresh_plan
GROUP BY 1
ORDER BY 1;

\if :apply
    WITH updated AS (
        UPDATE media
        SET metadata_status = plan.metadata_status,
            completeness_score = plan.completeness_score,
            next_refresh_at = plan.next_refresh_at
        FROM migrated_metadata_refresh_plan plan
        WHERE media.id = plan.id
          -- Worker 可能在计划生成后刚好刷新了这行；再次检查，绝不覆盖它的新状态。
          AND media.completeness_score = 0
          AND media.next_refresh_at IS NULL
          AND media.last_metadata_sync_at IS NULL
          AND media.content_hash = ''
          AND media.semantic_hash = ''
        RETURNING media.id
    )
    SELECT COUNT(*) AS updated_rows FROM updated;

    COMMIT;
    \echo '已提交初始化。现有 worker_jobs 未被修改。'
\else
    ROLLBACK;
    \echo '仅预览：已回滚。确认后使用 -v apply=1 重新执行。'
\endif
