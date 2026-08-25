-- 豆瓣按季建媒体页，而 TMDB 的 TV ID 指向整部剧。旧同步曾把一部剧的所有季度
-- 都写入某一个季度页面。保留单集身份和播放关联，只撤销错季的播出日期，并让
-- Worker 重新刷新对应主资料；新代码会按标题季数只写目标季度。
WITH season_media AS (
    SELECT id,
        LOWER(REGEXP_REPLACE(title, '第\s*[0-9一二三四五六七八九十两]+\s*季', '', 'g')) AS base_title,
        CASE
            WHEN original_title ~* '(?:season\s*|\ms)([0-9]{1,2})\M'
                THEN ((regexp_match(original_title, '(?i)(?:season\s*|\ms)([0-9]{1,2})\M'))[1])::integer
            WHEN title ~ '第\s*[0-9]{1,2}\s*季'
                THEN ((regexp_match(title, '第\s*([0-9]{1,2})\s*季'))[1])::integer
            WHEN title ~ '第\s*一\s*季' THEN 1
            WHEN title ~ '第\s*(两|二)\s*季' THEN 2
            WHEN title ~ '第\s*三\s*季' THEN 3
            WHEN title ~ '第\s*四\s*季' THEN 4
            WHEN title ~ '第\s*五\s*季' THEN 5
            WHEN title ~ '第\s*六\s*季' THEN 6
            WHEN title ~ '第\s*七\s*季' THEN 7
            WHEN title ~ '第\s*八\s*季' THEN 8
            WHEN title ~ '第\s*九\s*季' THEN 9
            WHEN title ~ '第\s*十\s*季' THEN 10
            WHEN title ~ '第\s*十一\s*季' THEN 11
            WHEN title ~ '第\s*十二\s*季' THEN 12
            ELSE 0
        END AS season_number
    FROM media
), affected_bases AS (
    SELECT DISTINCT scoped.base_title
    FROM media_units unit
    JOIN season_media scoped ON scoped.id = unit.media_id
    WHERE scoped.season_number > 0
      AND unit.season_number <> scoped.season_number
      AND unit.air_date IS NOT NULL
)
UPDATE media
SET next_refresh_at = NOW()
WHERE id IN (
    SELECT scoped.id
    FROM season_media scoped
    JOIN affected_bases affected ON affected.base_title = scoped.base_title
    WHERE scoped.season_number > 0 AND scoped.base_title <> ''
);

WITH season_media AS (
    SELECT id,
        CASE
            WHEN original_title ~* '(?:season\s*|\ms)([0-9]{1,2})\M'
                THEN ((regexp_match(original_title, '(?i)(?:season\s*|\ms)([0-9]{1,2})\M'))[1])::integer
            WHEN title ~ '第\s*[0-9]{1,2}\s*季'
                THEN ((regexp_match(title, '第\s*([0-9]{1,2})\s*季'))[1])::integer
            WHEN title ~ '第\s*一\s*季' THEN 1
            WHEN title ~ '第\s*(两|二)\s*季' THEN 2
            WHEN title ~ '第\s*三\s*季' THEN 3
            WHEN title ~ '第\s*四\s*季' THEN 4
            WHEN title ~ '第\s*五\s*季' THEN 5
            WHEN title ~ '第\s*六\s*季' THEN 6
            WHEN title ~ '第\s*七\s*季' THEN 7
            WHEN title ~ '第\s*八\s*季' THEN 8
            WHEN title ~ '第\s*九\s*季' THEN 9
            WHEN title ~ '第\s*十\s*季' THEN 10
            WHEN title ~ '第\s*十一\s*季' THEN 11
            WHEN title ~ '第\s*十二\s*季' THEN 12
            ELSE 0
        END AS season_number
    FROM media
)
UPDATE media_units unit
SET air_date = NULL, updated_at = NOW()
FROM season_media scoped
WHERE scoped.id = unit.media_id
  AND scoped.season_number > 0
  AND unit.season_number <> scoped.season_number
  AND unit.air_date IS NOT NULL;
