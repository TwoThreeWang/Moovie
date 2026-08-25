-- 把播放质量统计从独立的 rollup 表挪到 vod_items 上，按需由 worker 从明细表重算。

ALTER TABLE vod_items
  ADD COLUMN IF NOT EXISTS avg_speed_ms INTEGER NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS success_count INTEGER NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS failure_count INTEGER NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS quality_refreshed_at TIMESTAMPTZ;

UPDATE vod_items resource SET
  success_count = COALESCE(q.sc, 0),
  failure_count = COALESCE(q.fc, 0),
  avg_speed_ms = COALESCE(q.avg, 0),
  quality_refreshed_at = NOW()
FROM (
  SELECT line.source_key, line.vod_id,
         SUM(r.success_count)::INT AS sc,
         SUM(r.failure_count)::INT AS fc,
         CASE WHEN SUM(r.first_frame_count) > 0
              THEN (SUM(r.first_frame_total_ms) / SUM(r.first_frame_count))::INT
              ELSE 0 END AS avg
  FROM playback_quality_rollups r
  JOIN resource_episode_candidates c ON c.id = r.candidate_id
  JOIN resource_play_lines line ON line.id = c.line_id
  WHERE r.bucket >= NOW() - INTERVAL '7 days'
  GROUP BY line.source_key, line.vod_id
) q WHERE resource.source_key = q.source_key AND resource.vod_id = q.vod_id;

DROP TABLE playback_quality_rollups;
