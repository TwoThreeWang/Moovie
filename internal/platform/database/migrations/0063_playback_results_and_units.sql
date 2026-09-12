-- 播放地址只留在 vod_items；先迁移短期结果，后续迁移才移除旧外键。
SET LOCAL lock_timeout = '3s';

ALTER TABLE media ALTER COLUMN media_type SET DEFAULT '';
ALTER TABLE media_units ADD COLUMN has_resource BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE vod_items ADD COLUMN total_load_ms BIGINT NOT NULL DEFAULT 0;
ALTER TABLE vod_items ALTER COLUMN success_count TYPE BIGINT;
ALTER TABLE vod_items ALTER COLUMN failure_count TYPE BIGINT;

CREATE TABLE playback_results (
    attempt_id TEXT PRIMARY KEY,
    media_id BIGINT REFERENCES media(id) ON DELETE SET NULL,
    media_unit_id BIGINT REFERENCES media_units(id) ON DELETE SET NULL,
    source_key TEXT NOT NULL,
    vod_id TEXT NOT NULL,
    playback_version TEXT NOT NULL,
    succeeded BOOLEAN NOT NULL,
    load_ms INTEGER NOT NULL DEFAULT 0 CHECK (load_ms >= 0 AND load_ms <= 120000),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX playback_results_recent_idx ON playback_results(created_at,media_id);

-- 一次尝试只取先发生的终态；成功耗时必须来自同一次起播，不能拿旧平均值乘成功次数。
WITH terminal AS (
    SELECT DISTINCT ON(attempt_id) attempt_id, media_id, media_unit_id, source_key, vod_id,
           event_type='played_10s' AS succeeded, created_at
    FROM playback_attempt_events
    WHERE event_type IN ('played_10s','fatal_error') AND created_at >= NOW()-INTERVAL '30 days'
    ORDER BY attempt_id,created_at,id
), frames AS (
    SELECT attempt_id,MIN(elapsed_ms) AS load_ms FROM playback_attempt_events
    WHERE event_type='first_frame' AND elapsed_ms BETWEEN 1 AND 120000 GROUP BY attempt_id
)
INSERT INTO playback_results(attempt_id,media_id,media_unit_id,source_key,vod_id,playback_version,succeeded,load_ms,created_at)
SELECT t.attempt_id,t.media_id,t.media_unit_id,t.source_key,t.vod_id,md5(v.vod_play_url),t.succeeded,
       CASE WHEN t.succeeded THEN f.load_ms ELSE 0 END,t.created_at
FROM terminal t JOIN vod_items v USING(source_key,vod_id)
LEFT JOIN frames f USING(attempt_id)
WHERE NOT t.succeeded OR f.load_ms IS NOT NULL;

UPDATE vod_items SET total_load_ms=0,success_count=0,failure_count=0;
UPDATE vod_items v SET total_load_ms=q.total,success_count=q.successes,failure_count=q.failures
FROM (SELECT source_key,vod_id,SUM(load_ms)::BIGINT total,
      COUNT(*) FILTER(WHERE succeeded) successes,COUNT(*) FILTER(WHERE NOT succeeded) failures
      FROM playback_results GROUP BY source_key,vod_id) q
WHERE v.source_key=q.source_key AND v.vod_id=q.vod_id;

DELETE FROM worker_jobs WHERE task_type='quality_refresh';
