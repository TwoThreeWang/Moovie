-- Navicat 专用：初始化迁移媒体的完整度与刷新时间。
--
-- 第一次执行保持 FALSE：只生成计划和统计，updated_rows 必须为 0。
-- 确认统计合理后，把下面唯一的 FALSE 改成 TRUE，再完整执行一次提交更新。

BEGIN;

CREATE TEMP TABLE metadata_refresh_init_settings (
    apply BOOLEAN NOT NULL
) ON COMMIT DROP;

-- 唯一执行开关：FALSE = 只预览，TRUE = 提交更新。
INSERT INTO metadata_refresh_init_settings (apply) VALUES (FALSE);

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
        (CASE WHEN LOWER(BTRIM(media.title))      NOT IN ('', '[]', '{}', 'null') THEN 15 ELSE 0 END
       + CASE WHEN LOWER(BTRIM(media.year))       NOT IN ('', '[]', '{}', 'null') THEN  8 ELSE 0 END
       + CASE WHEN LOWER(BTRIM(media.media_type)) NOT IN ('', '[]', '{}', 'null') THEN  7 ELSE 0 END
       + CASE WHEN LOWER(BTRIM(media.poster))     NOT IN ('', '[]', '{}', 'null') THEN 10 ELSE 0 END
       + CASE WHEN LOWER(BTRIM(media.summary))    NOT IN ('', '[]', '{}', 'null') THEN 10 ELSE 0 END
       + CASE WHEN LOWER(BTRIM(media.genres))     NOT IN ('', '[]', '{}', 'null') THEN  8 ELSE 0 END
       + CASE WHEN LOWER(BTRIM(media.countries))  NOT IN ('', '[]', '{}', 'null') THEN  5 ELSE 0 END
       + CASE WHEN LOWER(BTRIM(media.directors))  NOT IN ('', '[]', '{}', 'null') THEN  8 ELSE 0 END
       + CASE WHEN LOWER(BTRIM(media.actors))     NOT IN ('', '[]', '{}', 'null') THEN  8 ELSE 0 END
       + CASE WHEN LOWER(BTRIM(media.duration))   NOT IN ('', '[]', '{}', 'null') THEN  5 ELSE 0 END
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
        WHEN scored.completeness_score < 70 THEN
            NOW() + INTERVAL '1 day'
            + MOD(scored.id * 7919, 30 * 24 * 60 * 60) * INTERVAL '1 second'
        ELSE
            NOW() + INTERVAL '30 days'
            + MOD(scored.id * 7919, 60 * 24 * 60 * 60) * INTERVAL '1 second'
    END AS next_refresh_at
FROM scored;

-- 结果集 1：计划摘要。
SELECT
    settings.apply,
    COUNT(plan.id) AS target_rows,
    COUNT(*) FILTER (WHERE plan.metadata_status = 'ready') AS ready_rows,
    COUNT(*) FILTER (WHERE plan.metadata_status = 'partial') AS partial_rows,
    COUNT(*) FILTER (WHERE plan.completeness_score < 70) AS below_70_rows,
    COUNT(*) FILTER (WHERE plan.completeness_score >= 70) AS at_least_70_rows,
    MIN(plan.next_refresh_at) AS earliest_refresh,
    MAX(plan.next_refresh_at) AS latest_refresh
FROM metadata_refresh_init_settings settings
LEFT JOIN migrated_metadata_refresh_plan plan ON TRUE
GROUP BY settings.apply;

-- 结果集 2：完整度分布。
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

-- 结果集 3：每日错峰量。
SELECT next_refresh_at::date AS refresh_date, COUNT(*) AS rows
FROM migrated_metadata_refresh_plan
GROUP BY 1
ORDER BY 1;

-- FALSE 时 WHERE 不成立，保证零更新；TRUE 时才写入。
WITH updated AS (
    UPDATE media
    SET metadata_status = plan.metadata_status,
        completeness_score = plan.completeness_score,
        next_refresh_at = plan.next_refresh_at
    FROM migrated_metadata_refresh_plan plan
    CROSS JOIN metadata_refresh_init_settings settings
    WHERE settings.apply
      AND media.id = plan.id
      -- Worker 可能刚好刷新了这行；再次检查，避免覆盖它的新状态。
      AND media.completeness_score = 0
      AND media.next_refresh_at IS NULL
      AND media.last_metadata_sync_at IS NULL
      AND media.content_hash = ''
      AND media.semantic_hash = ''
    RETURNING media.id
)
SELECT COUNT(*) AS updated_rows FROM updated;

COMMIT;
